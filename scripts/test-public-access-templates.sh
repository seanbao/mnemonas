#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TRAEFIK_DIR="$REPO_ROOT/deploy/public-access/traefik"
CLOUDFLARE_CONFIG="$REPO_ROOT/deploy/public-access/cloudflare-tunnel/config.yml"

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

test_traefik_template
test_cloudflare_tunnel_template

printf '[public-access-template-test] all checks passed\n'
