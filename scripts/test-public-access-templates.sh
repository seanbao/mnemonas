#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TRAEFIK_DIR="$REPO_ROOT/deploy/public-access/traefik"
CLOUDFLARE_CONFIG="$REPO_ROOT/deploy/public-access/cloudflare-tunnel/config.yml"
PUBLIC_ACCESS_README="$REPO_ROOT/deploy/public-access/README.md"
PUBLIC_ACCESS_README_EN="$REPO_ROOT/deploy/public-access/README.en.md"

fail() {
    printf '[public-access-template-test] ERROR: %s\n' "$*" >&2
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
        fail "$path contains unsafe text: $unexpected"
    fi
}

assert_tree_not_contains() {
    local path="$1"
    local unexpected="$2"
    if grep -R -Fq -- "$unexpected" "$path"; then
        fail "$path contains unsafe text: $unexpected"
    fi
}

test_traefik_template() {
    local compose="$TRAEFIK_DIR/docker-compose.yml"
    local static="$TRAEFIK_DIR/traefik.yml"
    local dynamic="$TRAEFIK_DIR/dynamic/mnemonas.yml"

    [[ -f "$compose" ]] || fail "missing Traefik compose template"
    [[ -f "$static" ]] || fail "missing Traefik static template"
    [[ -f "$dynamic" ]] || fail "missing Traefik dynamic template"

    assert_file_contains "$compose" "network_mode: host"
    assert_file_not_contains "$compose" "8080:"
    assert_file_not_contains "$compose" "9090:"
    assert_file_not_contains "$compose" "9091:"

    assert_file_contains "$static" "address: \":80\""
    assert_file_contains "$static" "address: \":443\""
    assert_file_contains "$static" "scheme: https"
    assert_file_contains "$static" "httpChallenge:"
    assert_file_contains "$static" "insecure: false"
    assert_tree_not_contains "$TRAEFIK_DIR" "api.insecure=true"

    assert_file_contains "$dynamic" "rule: \"Host(\`nas.example.com\`)\""
    assert_file_contains "$dynamic" "certResolver: letsencrypt"
    assert_file_contains "$dynamic" 'url: "http://127.0.0.1:8080"'
    assert_file_not_contains "$dynamic" "9090"
    assert_file_not_contains "$dynamic" "9091"
}

test_cloudflare_tunnel_template() {
    [[ -f "$CLOUDFLARE_CONFIG" ]] || fail "missing Cloudflare Tunnel config template"

    assert_file_contains "$CLOUDFLARE_CONFIG" "hostname: nas.example.com"
    assert_file_contains "$CLOUDFLARE_CONFIG" "service: http://127.0.0.1:8080"
    assert_file_contains "$CLOUDFLARE_CONFIG" "service: http_status:404"
    assert_file_not_contains "$CLOUDFLARE_CONFIG" "9090"
    assert_file_not_contains "$CLOUDFLARE_CONFIG" "9091"
}

test_public_access_readmes() {
    [[ -f "$PUBLIC_ACCESS_README" ]] || fail "missing public access Chinese README"
    [[ -f "$PUBLIC_ACCESS_README_EN" ]] || fail "missing public access English README"

    assert_file_contains "$PUBLIC_ACCESS_README" "公网访问模板"
    assert_file_contains "$PUBLIC_ACCESS_README" "[English](README.en.md)"
    assert_file_contains "$PUBLIC_ACCESS_README" "Traefik"
    assert_file_contains "$PUBLIC_ACCESS_README" "Cloudflare Tunnel"
    assert_file_contains "$PUBLIC_ACCESS_README" "只开放 \`80/443\`"
    assert_file_contains "$PUBLIC_ACCESS_README" "小写、无单个 FQDN 尾点"

    assert_file_contains "$PUBLIC_ACCESS_README_EN" "Public Access Templates"
    assert_file_contains "$PUBLIC_ACCESS_README_EN" "[简体中文](README.md)"
    assert_file_contains "$PUBLIC_ACCESS_README_EN" "Traefik"
    assert_file_contains "$PUBLIC_ACCESS_README_EN" "Cloudflare Tunnel"
    assert_file_contains "$PUBLIC_ACCESS_README_EN" "only \`80/443\`"
    assert_file_contains "$PUBLIC_ACCESS_README_EN" "lowercase and without a single FQDN trailing dot"
}

test_docker_docs_mount_syntax() {
    assert_tree_not_contains "$REPO_ROOT/docs" ".mnemonas:/data"
}

test_docs_avoid_webdav_placeholder_passwords() {
    assert_tree_not_contains "$REPO_ROOT/docs" "change-this-strong-password"
    assert_tree_not_contains "$REPO_ROOT/docs" "very-strong-password-here"
    assert_file_not_contains "$REPO_ROOT/docs/public-server-quickstart.md" 'sudo cat /srv/mnemonas/secrets.json'
    assert_file_not_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" 'sudo cat /srv/mnemonas/secrets.json'
    assert_file_contains "$REPO_ROOT/docs/docker-deployment.md" 'password = ""'
    assert_file_contains "$REPO_ROOT/docs/docker-deployment.en.md" 'password = ""'
    assert_file_contains "$REPO_ROOT/docs/configuration.md" 'password = "" # 留空使用自动生成密码'
    assert_file_contains "$REPO_ROOT/docs/configuration.en.md" 'password = "" # leave empty to use generated credentials'
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" 'webdav_password'
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" 'webdav_password'
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" '不应复制到工单、聊天记录或日志中'
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" 'should not be copied into support requests, chats, or logs'
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "自定义 \`auth.users_file\` 时会检查该文件所在目录"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "a custom \`auth.users_file\` moves the checked password file location"
    assert_file_contains "$REPO_ROOT/docs/security.md" '密码管理器生成的随机强密码'
    assert_file_contains "$REPO_ROOT/docs/security.en.md" 'password-manager value'
}

test_traefik_template
test_cloudflare_tunnel_template
test_public_access_readmes
test_docker_docs_mount_syntax
test_docs_avoid_webdav_placeholder_passwords

printf '[public-access-template-test] all checks passed\n'
