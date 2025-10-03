#!/usr/bin/env bash
# MnemoNAS public HTTPS reverse-proxy setup helper.
# Usage: sudo ./scripts/setup-reverse-proxy.sh [--proxy caddy|nginx] <domain> [email]
# Example: sudo ./scripts/setup-reverse-proxy.sh --proxy caddy nas.example.com admin@example.com

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

DOMAIN=""
EMAIL=""
PROXY_TYPE="${MNEMONAS_PROXY_TYPE:-}"
CONFIG_PATH="${MNEMONAS_CONFIG_PATH:-/etc/mnemonas/config.toml}"
CONFIGURE_MNEMONAS="${MNEMONAS_CONFIGURE_MNEMONAS:-1}"
CONFIGURE_FIREWALL="${MNEMONAS_CONFIGURE_FIREWALL:-1}"
RESTART_MNEMONAS="${MNEMONAS_RESTART_MNEMONAS:-1}"
TRUSTED_PROXY_HOPS="${MNEMONAS_TRUSTED_PROXY_HOPS:-1}"
UPSTREAM_HOST="${MNEMONAS_UPSTREAM_HOST:-127.0.0.1}"
UPSTREAM_PORT="${MNEMONAS_UPSTREAM_PORT:-}"
UPSTREAM_ENDPOINT=""
SYSTEMD_DIR="${MNEMONAS_SYSTEMD_DIR:-/etc/systemd/system}"
DATAPLANE_GRPC_PORT="${MNEMONAS_DATAPLANE_GRPC_PORT:-}"
DATAPLANE_HTTP_PORT="${MNEMONAS_DATAPLANE_HTTP_PORT:-}"

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

fail() {
    log_error "$1"
    exit 1
}

usage() {
    cat <<EOF
用法: sudo $0 [选项] <域名> [邮箱]

选项:
  --proxy caddy|nginx          反向代理类型，默认交互选择；非交互时默认 caddy
  --config <path>              MnemoNAS 配置文件路径，默认 /etc/mnemonas/config.toml
  --skip-mnemonas-config       不自动修改 MnemoNAS server.host/trusted_proxy_hops
  --no-firewall                不修改 UFW 防火墙规则
  --no-restart                 修改配置后不自动重启 mnemonas.service
  -h, --help                   显示帮助

环境变量:
  MNEMONAS_UPSTREAM_HOST       代理后端主机，默认 127.0.0.1
  MNEMONAS_UPSTREAM_PORT       代理后端端口，默认读取 config.toml 的 server.port，否则 8080
  MNEMONAS_TRUSTED_PROXY_HOPS  写入 server.trusted_proxy_hops 的值，默认 1
  MNEMONAS_DATAPLANE_GRPC_PORT 数据面 gRPC 端口，默认读取 config.toml，否则 9090
  MNEMONAS_DATAPLANE_HTTP_PORT 数据面 HTTP 端口，默认读取 systemd 单元，否则 9091

示例:
  sudo $0 --proxy caddy nas.example.com admin@example.com
EOF
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || fail "缺少必要命令: $1"
}

domain_is_safe() {
    local domain="$1"
    local -a labels
    local label

    [[ -n "$domain" ]] || return 1
    [[ ${#domain} -le 253 ]] || return 1
    [[ "$domain" =~ ^[A-Za-z0-9.-]+$ ]] || return 1
    [[ "$domain" != .* ]] || return 1
    [[ "$domain" != *. ]] || return 1
    [[ "$domain" != *..* ]] || return 1

    IFS='.' read -r -a labels <<< "$domain"
    for label in "${labels[@]}"; do
        [[ -n "$label" ]] || return 1
        [[ ${#label} -le 63 ]] || return 1
        [[ "$label" =~ ^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?$ ]] || return 1
    done

    return 0
}

email_is_safe() {
    local email="$1"
    local local_part domain_part

    [[ -n "$email" ]] || return 1
    [[ ${#email} -le 254 ]] || return 1
    [[ "$email" =~ ^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+$ ]] || return 1

    local_part="${email%@*}"
    domain_part="${email#*@}"
    [[ -n "$local_part" ]] || return 1
    [[ "$local_part" != "$email" ]] || return 1
    [[ ${#local_part} -le 64 ]] || return 1
    domain_is_safe "$domain_part"
}

require_safe_plain_value() {
    local value="$1"
    local label="$2"

    [[ -n "$value" ]] || fail "$label 不能为空"
    [[ "$value" != *[[:space:]]* ]] || fail "$label 不能包含空白字符: $value"
    [[ "$value" != *\"* && "$value" != *\\* ]] || fail "$label 不能包含引号或反斜杠: $value"
}

is_valid_tcp_host() {
    local host="$1"
    local label
    local -a labels

    if [[ "$host" =~ ^\[(.+)\]$ ]]; then
        host="${BASH_REMATCH[1]}"
    fi

    host="${host%.}"
    [[ -n "$host" ]] || return 1
    [[ "$host" != *"["* && "$host" != *"]"* ]] || return 1

    if [[ "$host" == *:* ]]; then
        [[ "$host" =~ ^[0-9A-Fa-f:.]+$ ]]
        return
    fi

    [[ "${#host}" -le 253 ]] || return 1
    IFS='.' read -r -a labels <<< "$host"
    for label in "${labels[@]}"; do
        [[ -n "$label" && "${#label}" -le 63 ]] || return 1
        [[ "$label" != -* && "$label" != *- ]] || return 1
        [[ "$label" =~ ^[A-Za-z0-9-]+$ ]] || return 1
    done
    return 0
}

require_safe_upstream_host() {
    local value="$1"

    require_safe_plain_value "$value" "MNEMONAS_UPSTREAM_HOST"
    [[ "$value" != "*" ]] || fail "MNEMONAS_UPSTREAM_HOST 不能是通配监听地址"
    is_valid_tcp_host "$value" || fail "MNEMONAS_UPSTREAM_HOST 主机格式不安全: $value"
}

format_host_port_endpoint() {
    local host="$1"
    local port="$2"

    if [[ "$host" =~ ^\[.+\]$ ]]; then
        printf '%s:%s\n' "$host" "$port"
        return
    fi
    if [[ "$host" == *:* ]]; then
        printf '[%s]:%s\n' "$host" "$port"
        return
    fi
    printf '%s:%s\n' "$host" "$port"
}

config_path_has_symlink_component() {
    local path="$1"
    local current="/"
    local part
    local -a parts

    IFS='/' read -r -a parts <<< "${path#/}"
    for part in "${parts[@]}"; do
        [[ -n "$part" ]] || continue

        if [[ "$current" == "/" ]]; then
            current="/$part"
        else
            current="$current/$part"
        fi

        if [[ -L "$current" ]]; then
            return 0
        fi
        if [[ ! -e "$current" ]]; then
            return 1
        fi
    done

    return 1
}

require_safe_config_path() {
    [[ "$CONFIG_PATH" == /* ]] || fail "--config 必须是绝对路径: $CONFIG_PATH"
    [[ "$CONFIG_PATH" != *[[:space:]]* ]] || fail "--config 不能包含空白字符: $CONFIG_PATH"
    if config_path_has_symlink_component "$CONFIG_PATH"; then
        fail "--config 不能包含符号链接: $CONFIG_PATH"
    fi
}

require_safe_port() {
    local value="$1"
    local label="$2"

    [[ "$value" =~ ^[0-9]+$ ]] || fail "$label 必须是数字端口: $value"
    (( 10#$value >= 1 && 10#$value <= 65535 )) || fail "$label 必须在 1-65535 之间: $value"
}

normalize_port() {
    local value="$1"

    printf '%s\n' "$((10#$value))"
}

toml_value() {
    local section="$1"
    local key="$2"
    local file="$3"
    awk -v section="[$section]" -v key="$key" '
        function strip_comment(text,    i, c, quote, escaped, out) {
            quote = ""
            escaped = 0
            out = ""
            for (i = 1; i <= length(text); i++) {
                c = substr(text, i, 1)
                if (quote == "\"") {
                    out = out c
                    if (escaped) {
                        escaped = 0
                        continue
                    }
                    if (c == "\\") {
                        escaped = 1
                        continue
                    }
                    if (c == quote) {
                        quote = ""
                    }
                    continue
                }
                if (quote == "\047") {
                    out = out c
                    if (c == quote) {
                        quote = ""
                    }
                    continue
                }
                if (c == "\"" || c == "\047") {
                    quote = c
                    out = out c
                    continue
                }
                if (c == "#") {
                    break
                }
                out = out c
            }
            return out
        }
        {
            line = strip_comment($0)
            gsub("^[[:space:]]+|[[:space:]]+$", "", line)
            section_line = line
            if (section_line ~ "^\\[") {
                sub("^\\[[[:space:]]*", "[", section_line)
                sub("[[:space:]]*\\]$", "]", section_line)
                gsub("[[:space:]]*\\.[[:space:]]*", ".", section_line)
            }
        }
        section_line == section {
            in_section = 1
            next
        }
        section_line ~ "^\\[" {
            in_section = 0
        }
        in_section && line ~ "^[[:space:]]*" key "[[:space:]]*=" {
            sub("^[[:space:]]*" key "[[:space:]]*=[[:space:]]*", "", line)
            gsub("^[[:space:]]+|[[:space:]]+$", "", line)
            gsub("^\"|\"$", "", line)
            gsub("^\047|\047$", "", line)
            print line
            exit
        }
    ' "$file"
}

systemd_env_value() {
    local key="$1"
    local file="$2"

    [[ -f "$file" ]] || return 0
    awk -v key="$key" '
        /^[[:space:]]*Environment=/ {
            line = $0
            sub(/^[[:space:]]*Environment=/, "", line)
            count = split(line, parts, /[[:space:]]+/)
            for (i = 1; i <= count; i++) {
                item = parts[i]
                gsub(/^"|"$/, "", item)
                if (substr(item, 1, length(key) + 1) == key "=") {
                    sub("^" key "=", "", item)
                    print item
                    exit
                }
            }
        }
    ' "$file"
}

tcp_addr_port() {
    local value="$1"

    if [[ "$value" =~ ^\[([^][]+)\]:([0-9]+)$ ]]; then
        printf '%s\n' "${BASH_REMATCH[2]}"
        return 0
    fi
    if [[ "$value" =~ ^[^:]+:([0-9]+)$ ]]; then
        printf '%s\n' "${BASH_REMATCH[1]}"
        return 0
    fi
    return 1
}

sed_replacement_escape() {
    sed 's/[\/&]/\\&/g' <<< "$1"
}

parse_args() {
    local -a positional=()

    while [[ $# -gt 0 ]]; do
        case "$1" in
            --proxy)
                [[ $# -ge 2 ]] || fail "--proxy 需要 caddy 或 nginx"
                PROXY_TYPE="$2"
                shift 2
                ;;
            --config)
                [[ $# -ge 2 ]] || fail "--config 需要路径"
                CONFIG_PATH="$2"
                shift 2
                ;;
            --skip-mnemonas-config)
                CONFIGURE_MNEMONAS=0
                shift
                ;;
            --no-firewall)
                CONFIGURE_FIREWALL=0
                shift
                ;;
            --no-restart)
                RESTART_MNEMONAS=0
                shift
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            --)
                shift
                break
                ;;
            -*)
                fail "未知参数: $1"
                ;;
            *)
                positional+=("$1")
                shift
                ;;
        esac
    done

    while [[ $# -gt 0 ]]; do
        positional+=("$1")
        shift
    done

    DOMAIN="${positional[0]:-}"
    EMAIL="${positional[1]:-}"
    [[ "${#positional[@]}" -le 2 ]] || fail "参数过多"
}

validate_inputs_before_root() {
    if [[ -z "$DOMAIN" ]]; then
        usage
        exit 1
    fi

    domain_is_safe "$DOMAIN" || fail "域名格式不安全: $DOMAIN"

    if [[ -z "$EMAIL" ]]; then
        EMAIL="admin@${DOMAIN}"
        log_warn "未指定邮箱，使用默认: $EMAIL"
    fi
    email_is_safe "$EMAIL" || fail "邮箱格式不安全: $EMAIL"

    if [[ -n "$PROXY_TYPE" && "$PROXY_TYPE" != "caddy" && "$PROXY_TYPE" != "nginx" ]]; then
        fail "--proxy 只能是 caddy 或 nginx"
    fi

    require_safe_config_path
    require_safe_upstream_host "$UPSTREAM_HOST"
    [[ "$TRUSTED_PROXY_HOPS" =~ ^[0-9]+$ ]] || fail "MNEMONAS_TRUSTED_PROXY_HOPS 必须是非负整数"
    (( 10#$TRUSTED_PROXY_HOPS >= 1 )) || fail "公网反向代理至少需要 trusted_proxy_hops = 1"
}

resolve_upstream_port() {
    local configured_port=""

    if [[ -z "$UPSTREAM_PORT" && -f "$CONFIG_PATH" ]]; then
        configured_port="$(toml_value server port "$CONFIG_PATH" || true)"
    fi
    UPSTREAM_PORT="${UPSTREAM_PORT:-${configured_port:-8080}}"
    require_safe_port "$UPSTREAM_PORT" "MNEMONAS_UPSTREAM_PORT"
    UPSTREAM_PORT="$(normalize_port "$UPSTREAM_PORT")"
    UPSTREAM_ENDPOINT="$(format_host_port_endpoint "$UPSTREAM_HOST" "$UPSTREAM_PORT")"
}

resolve_dataplane_ports() {
    local configured_grpc_address=""
    local configured_http_address=""
    local configured_grpc_port=""
    local configured_http_port=""

    if [[ -f "$CONFIG_PATH" ]]; then
        configured_grpc_address="$(toml_value dataplane grpc_address "$CONFIG_PATH" || true)"
    fi
    if [[ -n "$configured_grpc_address" ]]; then
        configured_grpc_port="$(tcp_addr_port "$configured_grpc_address" || true)"
        [[ -n "$configured_grpc_port" ]] || fail "dataplane.grpc_address 不是 host:port: $configured_grpc_address"
    fi

    configured_http_address="$(systemd_env_value DATAPLANE_HTTP_ADDR "$SYSTEMD_DIR/mnemonas-dataplane.service")"
    if [[ -n "$configured_http_address" ]]; then
        configured_http_port="$(tcp_addr_port "$configured_http_address" || true)"
        [[ -n "$configured_http_port" ]] || fail "DATAPLANE_HTTP_ADDR 不是 host:port: $configured_http_address"
    fi

    DATAPLANE_GRPC_PORT="${DATAPLANE_GRPC_PORT:-${configured_grpc_port:-9090}}"
    DATAPLANE_HTTP_PORT="${DATAPLANE_HTTP_PORT:-${configured_http_port:-9091}}"
    require_safe_port "$DATAPLANE_GRPC_PORT" "MNEMONAS_DATAPLANE_GRPC_PORT"
    require_safe_port "$DATAPLANE_HTTP_PORT" "MNEMONAS_DATAPLANE_HTTP_PORT"
    DATAPLANE_GRPC_PORT="$(normalize_port "$DATAPLANE_GRPC_PORT")"
    DATAPLANE_HTTP_PORT="$(normalize_port "$DATAPLANE_HTTP_PORT")"
}

validate_public_setup_ports() {
    case "$UPSTREAM_PORT" in
        80|443)
            fail "MNEMONAS_UPSTREAM_PORT 不能是 80 或 443；这些端口需要留给公网 HTTPS 反向代理"
            ;;
    esac
    case "$DATAPLANE_GRPC_PORT" in
        80|443)
            fail "MNEMONAS_DATAPLANE_GRPC_PORT 不能是 80 或 443；dataplane 端口必须保持内部访问"
            ;;
    esac
    case "$DATAPLANE_HTTP_PORT" in
        80|443)
            fail "MNEMONAS_DATAPLANE_HTTP_PORT 不能是 80 或 443；dataplane 端口必须保持内部访问"
            ;;
    esac
    [[ "$UPSTREAM_PORT" != "$DATAPLANE_GRPC_PORT" ]] || fail "MNEMONAS_UPSTREAM_PORT 不能和 MNEMONAS_DATAPLANE_GRPC_PORT 相同"
    [[ "$UPSTREAM_PORT" != "$DATAPLANE_HTTP_PORT" ]] || fail "MNEMONAS_UPSTREAM_PORT 不能和 MNEMONAS_DATAPLANE_HTTP_PORT 相同"
    [[ "$DATAPLANE_GRPC_PORT" != "$DATAPLANE_HTTP_PORT" ]] || fail "MNEMONAS_DATAPLANE_GRPC_PORT 不能和 MNEMONAS_DATAPLANE_HTTP_PORT 相同"
}

require_root() {
    [[ $EUID -eq 0 ]] || fail "请使用 sudo 运行此脚本"
}

warn_if_not_ubuntu() {
    if ! grep -q "Ubuntu" /etc/os-release 2>/dev/null; then
        log_warn "非 Ubuntu 系统，安装命令可能不兼容；如需手动配置，请参考 docs/public-server-quickstart.md"
    fi
}

select_proxy() {
    local choice=""

    if [[ -n "$PROXY_TYPE" ]]; then
        log_info "选择方案: $PROXY_TYPE"
        return
    fi

    if [[ -t 0 ]]; then
        echo ""
        echo "请选择反向代理方案:"
        echo "  1) Caddy (推荐，自动 HTTPS)"
        echo "  2) Nginx + Certbot"
        echo ""
        read -r -p "选择 [1/2]: " choice
    else
        choice="1"
        log_warn "非交互环境，默认使用 Caddy"
    fi

    case "$choice" in
        1|"")
            PROXY_TYPE="caddy"
            ;;
        2)
            PROXY_TYPE="nginx"
            ;;
        *)
            fail "无效选择"
            ;;
    esac

    log_info "选择方案: $PROXY_TYPE"
}

update_mnemonas_server_config() {
    local path="$1"
    local host="$2"
    local hops="$3"
    local tmp

    tmp="$(mktemp)"
    awk -v host="$host" -v hops="$hops" '
        function trim(text) {
            gsub("^[[:space:]]+|[[:space:]]+$", "", text)
            return text
        }
        function emit_missing() {
            if (in_server) {
                if (!host_seen) {
                    print "host = \"" host "\""
                }
                if (!hops_seen) {
                    print "trusted_proxy_hops = " hops
                }
            }
        }
        {
            trimmed = trim($0)
            if (trimmed ~ "^\\[") {
                if (in_server) {
                    emit_missing()
                    in_server = 0
                }
                if (trimmed == "[server]") {
                    server_seen = 1
                    in_server = 1
                    host_seen = 0
                    hops_seen = 0
                }
                print
                next
            }

            if (in_server && trimmed ~ "^host[[:space:]]*=") {
                print "host = \"" host "\""
                host_seen = 1
                next
            }

            if (in_server && trimmed ~ "^trusted_proxy_hops[[:space:]]*=") {
                print "trusted_proxy_hops = " hops
                hops_seen = 1
                next
            }

            print
        }
        END {
            if (in_server) {
                emit_missing()
            }
            if (!server_seen) {
                print ""
                print "[server]"
                print "host = \"" host "\""
                print "trusted_proxy_hops = " hops
            }
        }
    ' "$path" > "$tmp"

    cat "$tmp" > "$path"
    rm -f -- "$tmp"
}

configure_mnemonas() {
    local backup=""
    local check_log=""

    if [[ "$CONFIGURE_MNEMONAS" != "1" ]]; then
        log_warn "已跳过 MnemoNAS 配置修改；请手动设置 server.host 和 trusted_proxy_hops"
        return
    fi

    if [[ ! -f "$CONFIG_PATH" ]]; then
        log_warn "未找到 MnemoNAS 配置: $CONFIG_PATH"
        log_warn "请手动加入: [server] host = \"$UPSTREAM_HOST\", trusted_proxy_hops = $TRUSTED_PROXY_HOPS"
        return
    fi

    backup="${CONFIG_PATH}.bak.$(date +%Y%m%d%H%M%S)"
    cp "$CONFIG_PATH" "$backup"
    log_info "已备份 MnemoNAS 配置: $backup"

    update_mnemonas_server_config "$CONFIG_PATH" "$UPSTREAM_HOST" "$TRUSTED_PROXY_HOPS"
    log_info "已设置 MnemoNAS 后端监听: $UPSTREAM_ENDPOINT"
    log_info "已设置 server.trusted_proxy_hops = $TRUSTED_PROXY_HOPS"

    if command -v nasd >/dev/null 2>&1; then
        check_log="$(mktemp)"
        if nasd --check-config --config "$CONFIG_PATH" >"$check_log" 2>&1; then
            log_info "MnemoNAS 配置校验通过"
        else
            mv "$backup" "$CONFIG_PATH"
            cat "$check_log" >&2 || true
            rm -f -- "$check_log"
            fail "MnemoNAS 配置校验失败，已恢复备份"
        fi
        rm -f -- "$check_log"
    else
        log_warn "未找到 nasd，跳过配置校验"
    fi

    if [[ "$RESTART_MNEMONAS" != "1" ]]; then
        log_warn "已跳过 mnemonas.service 重启；请稍后手动重启"
        return
    fi

    if command -v systemctl >/dev/null 2>&1 && systemctl list-unit-files mnemonas.service >/dev/null 2>&1; then
        systemctl restart mnemonas.service
        log_info "已重启 mnemonas.service"
    else
        log_warn "未检测到 mnemonas.service；如果不是 systemd 部署，请手动重启 MnemoNAS"
    fi
}

install_cron_line_once() {
    local line="$1"
    local current_cron
    current_cron="$(mktemp)"

    crontab -l > "$current_cron" 2>/dev/null || true
    if grep -Fqx "$line" "$current_cron"; then
        log_info "证书续期 cron 已存在，跳过重复写入"
    else
        { cat "$current_cron"; echo "$line"; } | crontab -
        log_info "已写入证书续期 cron"
    fi
    rm -f -- "$current_cron"
}

configure_certbot_renewal() {
    if systemctl list-unit-files certbot.timer >/dev/null 2>&1; then
        systemctl enable --now certbot.timer >/dev/null
        log_info "已启用 certbot.timer 自动续期"
        return 0
    fi

    if command -v crontab >/dev/null 2>&1; then
        install_cron_line_once "0 3 * * * certbot renew --quiet --post-hook 'systemctl reload nginx'"
        return 0
    fi

    log_warn "未检测到 certbot.timer 或 crontab；请手动配置 certbot renew 自动续期"
}

install_caddy() {
    log_info "安装 Caddy..."

    apt-get update
    apt-get install -y debian-keyring debian-archive-keyring apt-transport-https curl gnupg

    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | \
        gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | \
        tee /etc/apt/sources.list.d/caddy-stable.list

    apt-get update
    apt-get install -y caddy

    log_info "配置 Caddyfile..."
    if [[ -f /etc/caddy/Caddyfile ]]; then
        cp /etc/caddy/Caddyfile "/etc/caddy/Caddyfile.bak.$(date +%Y%m%d%H%M%S)"
    fi

    cat > /etc/caddy/Caddyfile << EOF
# MnemoNAS public HTTPS reverse proxy
# Generated at: $(date)

$DOMAIN {
    tls $EMAIL

    reverse_proxy $UPSTREAM_ENDPOINT {
        header_up Host {host}
        header_up X-Real-IP {remote_host}
        header_up X-Forwarded-For {remote_host}
        header_up X-Forwarded-Proto {scheme}
    }

    request_body {
        max_size 10GB
    }

    log {
        output file /var/log/caddy/mnemonas-access.log {
            roll_size 100mb
            roll_keep 5
        }
        format json
    }
}
EOF

    mkdir -p /var/log/caddy
    chown caddy:caddy /var/log/caddy

    log_info "验证 Caddy 配置..."
    caddy validate --config /etc/caddy/Caddyfile

    systemctl enable caddy
    systemctl restart caddy

    log_info "Caddy 配置完成"
}

install_nginx() {
    local domain_escaped upstream_escaped generated_escaped

    log_info "安装 Nginx 和 Certbot..."
    apt-get update
    apt-get install -y nginx certbot python3-certbot-nginx

    log_info "配置 Nginx..."

    cat > "/etc/nginx/sites-available/$DOMAIN" << 'EOF'
# MnemoNAS public HTTPS reverse proxy
# Generated at: GENERATED_TIME

server {
    listen 80;
    server_name DOMAIN_PLACEHOLDER;

    location /.well-known/acme-challenge/ {
        root /var/www/certbot;
    }

    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl http2;
    server_name DOMAIN_PLACEHOLDER;

    ssl_certificate /etc/letsencrypt/live/DOMAIN_PLACEHOLDER/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/DOMAIN_PLACEHOLDER/privkey.pem;

    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384;
    ssl_prefer_server_ciphers off;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 1d;

    add_header Strict-Transport-Security "max-age=63072000" always;

    client_max_body_size 10G;
    client_body_timeout 3600s;
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
    proxy_buffering off;
    proxy_request_buffering off;

    location / {
        proxy_pass http://UPSTREAM_PLACEHOLDER;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_pass_request_headers on;
        proxy_set_header Destination $http_destination;

        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }

    access_log /var/log/nginx/DOMAIN_PLACEHOLDER.access.log;
    error_log /var/log/nginx/DOMAIN_PLACEHOLDER.error.log;
}
EOF

    domain_escaped="$(sed_replacement_escape "$DOMAIN")"
    upstream_escaped="$(sed_replacement_escape "$UPSTREAM_ENDPOINT")"
    generated_escaped="$(sed_replacement_escape "$(date)")"
    sed -i "s/DOMAIN_PLACEHOLDER/$domain_escaped/g" "/etc/nginx/sites-available/$DOMAIN"
    sed -i "s/UPSTREAM_PLACEHOLDER/$upstream_escaped/g" "/etc/nginx/sites-available/$DOMAIN"
    sed -i "s/GENERATED_TIME/$generated_escaped/g" "/etc/nginx/sites-available/$DOMAIN"

    mkdir -p /var/www/certbot
    ln -sf "/etc/nginx/sites-available/$DOMAIN" /etc/nginx/sites-enabled/
    rm -f -- /etc/nginx/sites-enabled/default

    cat > "/etc/nginx/sites-available/$DOMAIN.temp" << EOF
server {
    listen 80;
    server_name $DOMAIN;

    location /.well-known/acme-challenge/ {
        root /var/www/certbot;
    }

    location / {
        return 200 'MnemoNAS setup in progress';
        add_header Content-Type text/plain;
    }
}
EOF

    ln -sf "/etc/nginx/sites-available/$DOMAIN.temp" "/etc/nginx/sites-enabled/$DOMAIN"
    nginx -t
    systemctl enable nginx
    systemctl restart nginx

    log_info "申请 Let's Encrypt 证书..."
    certbot certonly --webroot -w /var/www/certbot \
        -d "$DOMAIN" \
        --email "$EMAIL" \
        --agree-tos \
        --non-interactive

    ln -sf "/etc/nginx/sites-available/$DOMAIN" "/etc/nginx/sites-enabled/$DOMAIN"
    rm -f -- "/etc/nginx/sites-available/$DOMAIN.temp"

    nginx -t
    systemctl reload nginx

    log_info "配置证书自动续期..."
    configure_certbot_renewal

    log_info "Nginx + Certbot 配置完成"
}

configure_firewall() {
    if [[ "$CONFIGURE_FIREWALL" != "1" ]]; then
        log_warn "已跳过防火墙配置；请确认公网安全组只开放 80/443"
        return
    fi

    log_info "配置本机 UFW 防火墙规则..."
    if command -v ufw >/dev/null 2>&1; then
        ufw allow 80/tcp
        ufw allow 443/tcp
        if [[ "$UPSTREAM_PORT" != "80" && "$UPSTREAM_PORT" != "443" ]]; then
            ufw deny "$UPSTREAM_PORT/tcp" comment "MnemoNAS direct HTTP"
        fi
        ufw deny "$DATAPLANE_GRPC_PORT/tcp" comment "MnemoNAS dataplane gRPC"
        ufw deny "$DATAPLANE_HTTP_PORT/tcp" comment "MnemoNAS dataplane HTTP"
        log_info "已允许 80/443，并限制 $UPSTREAM_PORT/$DATAPLANE_GRPC_PORT/$DATAPLANE_HTTP_PORT"
    else
        log_warn "未检测到 ufw；请在云安全组或系统防火墙中只开放 80/443"
    fi

    log_warn "此脚本不能修改云厂商安全组；请确认公网只开放 TCP 80/443，SSH 仅限可信来源"
}

ss_local_addresses_for_port() {
    local port="$1"
    ss -lntH 2>/dev/null | awk -v suffix=":$port" '$4 ~ suffix "$" { print $4 }'
}

host_from_ss_local_address() {
    local address="$1"
    local port="$2"
    local host="$address"

    if [[ "$host" == *":$port" ]]; then
        host="${host%:"$port"}"
    fi
    host="${host#\[}"
    host="${host%\]}"
    printf '%s\n' "$host"
}

is_loopback_host() {
    local host="$1"
    case "$host" in
        localhost|ip6-localhost|127.*|::1)
            return 0
            ;;
        *)
            return 1
            ;;
    esac
}

check_loopback_only_port() {
    local port="$1"
    local label="$2"
    local address host
    local -a unsafe_addresses=()

    if ! command -v ss >/dev/null 2>&1; then
        log_warn "ss 不可用，跳过 $label 端口检查"
        return
    fi

    while IFS= read -r address; do
        [[ -n "$address" ]] || continue
        host="$(host_from_ss_local_address "$address" "$port")"
        if ! is_loopback_host "$host"; then
            unsafe_addresses+=("$address")
        fi
    done < <(ss_local_addresses_for_port "$port")

    if [[ "${#unsafe_addresses[@]}" -eq 0 ]]; then
        log_info "$label 端口 $port 仅本机监听或未监听"
    else
        log_warn "$label 端口 $port 仍监听在非 loopback 地址: ${unsafe_addresses[*]}"
    fi
}

run_post_setup_checks() {
    log_info "运行公网入口检查..."

    check_loopback_only_port "$UPSTREAM_PORT" "MnemoNAS Web/API/WebDAV"
    check_loopback_only_port "$DATAPLANE_GRPC_PORT" "MnemoNAS dataplane gRPC"
    check_loopback_only_port "$DATAPLANE_HTTP_PORT" "MnemoNAS dataplane HTTP"

    if command -v curl >/dev/null 2>&1; then
        if curl -fsSI "https://$DOMAIN/health" >/dev/null 2>&1; then
            log_info "HTTPS 健康检查通过: https://$DOMAIN/health"
        else
            log_warn "HTTPS 健康检查暂未通过；请确认 DNS 已解析到本机、公网 80/443 已放行、证书申请完成"
        fi
    fi

    if command -v mnemonas-doctor >/dev/null 2>&1; then
        if SERVER_URL="http://$UPSTREAM_ENDPOINT" DATAPLANE_GRPC_PORT="$DATAPLANE_GRPC_PORT" DATAPLANE_HTTP_PORT="$DATAPLANE_HTTP_PORT" mnemonas-doctor --public-domain "$DOMAIN"; then
            log_info "mnemonas-doctor 检查通过"
        else
            log_warn "mnemonas-doctor 存在失败或警告，请按输出处理后再开放给真实用户"
        fi
    else
        log_warn "未找到 mnemonas-doctor，跳过部署诊断"
    fi
}

print_summary() {
    echo ""
    log_info "=========================================="
    log_info "公网 HTTPS 入口配置完成"
    log_info "=========================================="
    echo ""
    echo "访问地址: https://$DOMAIN"
    echo "WebDAV:   https://$DOMAIN/dav"
    echo "后端入口: http://$UPSTREAM_ENDPOINT"
    echo ""
    echo "已处理:"
    echo "  - 反向代理: $PROXY_TYPE"
    echo "  - MnemoNAS 配置: server.host = \"$UPSTREAM_HOST\", trusted_proxy_hops = $TRUSTED_PROXY_HOPS"
    echo "  - 本机防火墙: 允许 80/443，限制直接访问 $UPSTREAM_PORT/$DATAPLANE_GRPC_PORT/$DATAPLANE_HTTP_PORT"
    echo ""
    echo "仍需人工确认:"
    echo "  - 云厂商安全组只开放 80/443；SSH 限制到可信 IP 或私有网络"
    echo "  - 首次登录后已修改管理员密码"
    echo "  - 已配置独立备份，不把公网服务作为唯一数据副本"
    echo ""
    echo "验证命令:"
    echo "  curl -I https://$DOMAIN/health"
    echo "  curl --connect-timeout 3 http://$DOMAIN:$UPSTREAM_PORT/health  # 应失败或超时"
    echo "  WEBDAV_USER=<webdav-username>"
    echo "  WEBDAV_PASS=<webdav-password>"
    echo "  curl -u \"\$WEBDAV_USER:\$WEBDAV_PASS\" -X PROPFIND https://$DOMAIN/dav/ -H 'Depth: 0'"
    echo ""

    if [[ "$PROXY_TYPE" == "caddy" ]]; then
        echo "管理命令:"
        echo "  systemctl status caddy"
        echo "  journalctl -u caddy -f"
        echo "  caddy reload --config /etc/caddy/Caddyfile"
    else
        echo "管理命令:"
        echo "  systemctl status nginx"
        echo "  nginx -t"
        echo "  systemctl reload nginx"
        echo "  certbot certificates"
    fi
    echo ""
}

run_self_test() {
    local tmp config
    tmp="$(mktemp -d)"
    trap 'rm -rf -- "$tmp"' RETURN

    config="$tmp/config.toml"
    cat > "$config" <<'EOF'
[server]
host = "0.0.0.0"
port = 8080

[server.tls]
enabled = false
EOF
    update_mnemonas_server_config "$config" "127.0.0.1" "1"
    grep -Fq 'host = "127.0.0.1"' "$config" || fail "self-test failed: host was not updated"
    grep -Fq 'trusted_proxy_hops = 1' "$config" || fail "self-test failed: trusted_proxy_hops was not inserted"

    config="$tmp/missing-server.toml"
    cat > "$config" <<'EOF'
[storage]
root = "/srv/mnemonas"
EOF
    update_mnemonas_server_config "$config" "127.0.0.1" "1"
    grep -Fq '[server]' "$config" || fail "self-test failed: server section was not appended"
    grep -Fq 'host = "127.0.0.1"' "$config" || fail "self-test failed: appended host missing"

    [[ "$(tcp_addr_port '127.0.0.1:19090')" == "19090" ]] || fail "self-test failed: IPv4 port parsing"
    [[ "$(tcp_addr_port '[::1]:19091')" == "19091" ]] || fail "self-test failed: IPv6 port parsing"
    [[ "$(normalize_port '0443')" == "443" ]] || fail "self-test failed: port normalization"
    is_valid_tcp_host "127.0.0.1" || fail "self-test failed: IPv4 host validation"
    is_valid_tcp_host "localhost" || fail "self-test failed: localhost host validation"
    is_valid_tcp_host "nas.example.com" || fail "self-test failed: hostname validation"
    is_valid_tcp_host "::1" || fail "self-test failed: raw IPv6 host validation"
    is_valid_tcp_host "[::1]" || fail "self-test failed: bracketed IPv6 host validation"
    ! is_valid_tcp_host "bad-.example.com" || fail "self-test failed: invalid host accepted"
    ! is_valid_tcp_host "http://127.0.0.1" || fail "self-test failed: URL host accepted"
    [[ "$(format_host_port_endpoint '127.0.0.1' '8080')" == "127.0.0.1:8080" ]] || fail "self-test failed: IPv4 endpoint formatting"
    [[ "$(format_host_port_endpoint '::1' '8080')" == "[::1]:8080" ]] || fail "self-test failed: raw IPv6 endpoint formatting"
    [[ "$(format_host_port_endpoint '[::1]' '8080')" == "[::1]:8080" ]] || fail "self-test failed: bracketed IPv6 endpoint formatting"

    cat > "$tmp/mnemonas-dataplane.service" <<'EOF'
[Service]
Environment=DATAPLANE_HTTP_ADDR=127.0.0.1:19091
EOF
    [[ "$(systemd_env_value DATAPLANE_HTTP_ADDR "$tmp/mnemonas-dataplane.service")" == "127.0.0.1:19091" ]] || fail "self-test failed: systemd environment parsing"

    cat > "$tmp/mnemonas-dataplane-quoted.service" <<'EOF'
[Service]
Environment="DATAPLANE_HTTP_ADDR=[::1]:19092" "OTHER=value"
EOF
    [[ "$(systemd_env_value DATAPLANE_HTTP_ADDR "$tmp/mnemonas-dataplane-quoted.service")" == "[::1]:19092" ]] || fail "self-test failed: quoted systemd environment parsing"

    printf '[reverse-proxy-self-test] all checks passed\n'
}

main() {
    parse_args "$@"
    validate_inputs_before_root
    resolve_upstream_port
    resolve_dataplane_ports
    validate_public_setup_ports
    require_root
    require_command apt-get
    require_command awk
    require_command sed
    require_command systemctl
    warn_if_not_ubuntu
    select_proxy

    echo ""
    log_info "=========================================="
    log_info "MnemoNAS 公网 HTTPS 入口自动配置"
    log_info "=========================================="
    echo ""
    log_info "域名: $DOMAIN"
    log_info "证书邮箱: $EMAIL"
    log_info "后端: $UPSTREAM_ENDPOINT"
    log_info "配置文件: $CONFIG_PATH"

    configure_mnemonas
    configure_firewall

    if [[ "$PROXY_TYPE" == "caddy" ]]; then
        install_caddy
    else
        install_nginx
    fi

    run_post_setup_checks
    print_summary
}

if [[ "${MNEMONAS_REVERSE_PROXY_SELF_TEST:-0}" == "1" ]]; then
    run_self_test
    exit 0
fi

main "$@"
