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

compose_has_ports_section() {
    local path="$1"
    grep -Eq '^[[:space:]]*ports[[:space:]]*:' "$path"
}

assert_compose_has_no_ports_section() {
    local path="$1"
    if compose_has_ports_section "$path"; then
        fail "$path must not define a ports section; public Traefik templates use host networking and must not publish backend ports"
    fi
}

assert_tree_not_contains() {
    local path="$1"
    local unexpected="$2"
    if grep -R -Fq -- "$unexpected" "$path"; then
        fail "$path contains unsafe text: $unexpected"
    fi
}

test_public_compose_ports_detector() {
    local tmpdir
    local bad_compose

    tmpdir="$(mktemp -d)"
    bad_compose="$tmpdir/docker-compose.yml"

    cat >"$bad_compose" <<'YAML'
services:
  traefik:
    image: traefik:v3.0
    network_mode: host
    ports:
      - target: 9090
        published: 9090
        protocol: tcp
YAML

    if ! compose_has_ports_section "$bad_compose"; then
        rm -rf "$tmpdir"
        fail "public compose ports detector missed long-form ports"
    fi

    rm -rf "$tmpdir"
}

test_public_access_yaml_templates_are_valid() {
    "$REPO_ROOT/scripts/check-yaml-configs.sh" \
        "$TRAEFIK_DIR/docker-compose.yml" \
        "$TRAEFIK_DIR/traefik.yml" \
        "$TRAEFIK_DIR/dynamic/mnemonas.yml" \
        "$CLOUDFLARE_CONFIG" >/dev/null
}

test_traefik_template() {
    local compose="$TRAEFIK_DIR/docker-compose.yml"
    local static="$TRAEFIK_DIR/traefik.yml"
    local dynamic="$TRAEFIK_DIR/dynamic/mnemonas.yml"

    [[ -f "$compose" ]] || fail "missing Traefik compose template"
    [[ -f "$static" ]] || fail "missing Traefik static template"
    [[ -f "$dynamic" ]] || fail "missing Traefik dynamic template"

    assert_file_contains "$compose" "network_mode: host"
    assert_compose_has_no_ports_section "$compose"
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
    assert_file_contains "$PUBLIC_ACCESS_README" 'host = "127.0.0.1"'
    assert_file_contains "$PUBLIC_ACCESS_README" "trusted_proxy_hops = 1"
    assert_file_contains "$PUBLIC_ACCESS_README" 'base_url = "https://nas.example.com"'
    assert_file_contains "$PUBLIC_ACCESS_README" "trusted_proxy_cidrs"
    assert_file_contains "$PUBLIC_ACCESS_README" "反斜杠、重复路径斜杠或 \`.\`/\`..\` 路径段"
    assert_file_contains "$PUBLIC_ACCESS_README" "重复路径斜杠、\`.\`/\`..\` 路径段和无效主机名判为失败"
    assert_file_contains "$PUBLIC_ACCESS_README" "公网诊断依赖本机监听端口检查"
    assert_file_contains "$PUBLIC_ACCESS_README" "/proc/net/tcp6"
    assert_file_contains "$PUBLIC_ACCESS_README" "运行诊断的主机还必须安装 \`curl\`、\`python3\` 和 \`openssl\`"
    assert_file_contains "$PUBLIC_ACCESS_README" "[公网云防火墙复核清单](../../docs/cloud-firewall-checklist.md)"

    assert_file_contains "$PUBLIC_ACCESS_README_EN" "Public Access Templates"
    assert_file_contains "$PUBLIC_ACCESS_README_EN" "[简体中文](README.md)"
    assert_file_contains "$PUBLIC_ACCESS_README_EN" "Traefik"
    assert_file_contains "$PUBLIC_ACCESS_README_EN" "Cloudflare Tunnel"
    assert_file_contains "$PUBLIC_ACCESS_README_EN" "only \`80/443\`"
    assert_file_contains "$PUBLIC_ACCESS_README_EN" "lowercase and without a single FQDN trailing dot"
    assert_file_contains "$PUBLIC_ACCESS_README_EN" 'host = "127.0.0.1"'
    assert_file_contains "$PUBLIC_ACCESS_README_EN" "trusted_proxy_hops = 1"
    assert_file_contains "$PUBLIC_ACCESS_README_EN" 'base_url = "https://nas.example.com"'
    assert_file_contains "$PUBLIC_ACCESS_README_EN" "trusted_proxy_cidrs"
    assert_file_contains "$PUBLIC_ACCESS_README_EN" "backslash, duplicated path slash, or \`.\`/\`..\` path segments"
    assert_file_contains "$PUBLIC_ACCESS_README_EN" "duplicated path slashes, \`.\`/\`..\` path segments, and invalid hostnames"
    assert_file_contains "$PUBLIC_ACCESS_README_EN" "Public diagnostics depend on local listener inspection"
    assert_file_contains "$PUBLIC_ACCESS_README_EN" "/proc/net/tcp6"
    assert_file_contains "$PUBLIC_ACCESS_README_EN" "must also have \`curl\`, \`python3\`, and \`openssl\` installed"
    assert_file_contains "$PUBLIC_ACCESS_README_EN" "[Public cloud firewall checklist](../../docs/cloud-firewall-checklist.en.md)"
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
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "bcrypt 格式的 \`password_hash\`"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "bcrypt-format \`password_hash\`"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "用户文件及其目录不能是符号链接，路径组件也不能包含符号链接"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "users file and its directory must not be symlinks and must not pass through symlink components"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "符号链接、路径组件包含符号链接或该路径是非普通文件"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "symlink, symlink component, or non-regular file"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "\`secrets.json\` 必须存在，且必须是私有普通文件"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "该文件不能是符号链接，路径组件也不能包含符号链接"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "\`secrets.json\` must exist, be a private regular file"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "not be a symlink, and not pass through symlink path components"
    assert_file_contains "$REPO_ROOT/docs/security.md" '用户文件及其目录不是符号链接、路径组件不包含符号链接且权限为私有'
    assert_file_contains "$REPO_ROOT/docs/security.en.md" 'non-symlink users file and users-file directory that do not pass through symlink components and have private permissions'
    assert_file_contains "$REPO_ROOT/docs/security.md" '自动 WebDAV 凭据文件不是符号链接、路径组件不包含符号链接且权限私有'
    assert_file_contains "$REPO_ROOT/docs/security.en.md" 'non-symlink generated WebDAV credentials file that does not pass through symlink components and has private permissions'
    assert_file_contains "$REPO_ROOT/docs/security.md" '密码管理器生成的随机强密码'
    assert_file_contains "$REPO_ROOT/docs/security.en.md" 'password-manager value'
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" 'auth.access_token_ttl'
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" 'auth.access_token_ttl'
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" '/etc/mnemonas/config.toml` 是普通私有文件，路径组件不包含符号链接，且 TOML 语法有效'
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" '/etc/mnemonas/config.toml` is a private regular file, does not pass through symlink components, and parses as valid TOML'
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" 'share.default_expires_in'
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" 'share.default_expires_in'
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "share.default_max_access\` 大于 \`0\`"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "share.default_max_access\` is greater than \`0\`"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "运行 \`mnemonas-doctor --public-domain\` 的主机可使用 \`ss\`"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "The host running \`mnemonas-doctor --public-domain\` can use \`ss\`"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "运行 \`mnemonas-doctor --public-domain\` 的主机已安装 \`curl\`、\`python3\` 和 \`openssl\`"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "The host running \`mnemonas-doctor --public-domain\` has \`curl\`, \`python3\`, and \`openssl\` installed"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "公网 HTTP 必须返回到同一域名的 HTTPS 跳转"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "Public HTTP must redirect to HTTPS on the same domain"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "启用公开分享时，公开分享 API 探测必须到达 MnemoNAS"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "When public sharing is enabled, the public-share API probe must reach MnemoNAS"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "反斜杠、重复的路径斜杠或 \`.\`/\`..\` 路径段"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "backslash, duplicated path slash, or \`.\`/\`..\` path segments"
    assert_file_contains "$REPO_ROOT/docs/api-reference.md" "反斜杠、重复路径斜杠、\`.\`/\`..\` 路径段或非法主机名报告为 \`block\`"
    assert_file_contains "$REPO_ROOT/docs/api-reference.en.md" "backslashes, duplicated path slashes, \`.\`/\`..\` path segments, or an invalid host name is reported as \`block\`"
    assert_file_contains "$REPO_ROOT/docs/security.md" "反斜杠、重复路径斜杠或 \`.\`/\`..\` 路径段"
    assert_file_contains "$REPO_ROOT/docs/security.en.md" "backslash, duplicated path slash, or \`.\`/\`..\` path segments"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.md" "[公网云防火墙复核清单](cloud-firewall-checklist.md)"
    assert_file_contains "$REPO_ROOT/docs/public-server-quickstart.en.md" "[Public cloud firewall checklist](cloud-firewall-checklist.en.md)"
    assert_file_contains "$REPO_ROOT/docs/README.md" "[公网云防火墙复核清单](cloud-firewall-checklist.md)"
    assert_file_contains "$REPO_ROOT/docs/README.en.md" "[Public cloud firewall checklist](cloud-firewall-checklist.en.md)"
}

test_public_compose_ports_detector
test_public_access_yaml_templates_are_valid
test_traefik_template
test_cloudflare_tunnel_template
test_public_access_readmes
test_docker_docs_mount_syntax
test_docs_avoid_webdav_placeholder_passwords

printf '[public-access-template-test] all checks passed\n'
