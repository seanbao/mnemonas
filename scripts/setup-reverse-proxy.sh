#!/usr/bin/env bash
# MnemoNAS public HTTPS reverse-proxy setup helper.
# Usage: sudo ./scripts/setup-reverse-proxy.sh [--proxy caddy|nginx] <domain> [email]
# Example: sudo ./scripts/setup-reverse-proxy.sh --proxy caddy nas.example.com admin@example.com

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
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
WEBDAV_PREFIX=""
MNEMONAS_CONFIG_STATUS="pending"
FIREWALL_STATUS="pending"

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

run_apt_get() {
    local description="$1"
    shift

    if apt-get -o DPkg::Lock::Timeout=120 "$@"; then
        return 0
    fi

    fail "$description 失败；apt/dpkg 可能被其他进程占用，或软件源暂时不可用"
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

normalize_domain() {
    local domain="${1,,}"

    [[ -n "$domain" ]] || return 0
    if [[ "$domain" == *. ]]; then
        domain="${domain%.}"
        [[ "$domain" != *. ]] || return 1
    fi
    printf '%s\n' "$domain"
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
    [[ "$value" != *[[:cntrl:]]* ]] || fail "$label 不能包含控制字符: $value"
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

path_has_parent_segment() {
    local path="$1"
    local part
    local -a parts

    IFS='/' read -r -a parts <<< "$path"
    for part in "${parts[@]}"; do
        [[ "$part" != ".." ]] || return 0
    done
    return 1
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
    [[ "$CONFIG_PATH" != *[[:cntrl:]]* ]] || fail "--config 不能包含控制字符: $CONFIG_PATH"
    if path_has_parent_segment "$CONFIG_PATH"; then
        fail "--config 不能包含父目录段: $CONFIG_PATH"
    fi
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

normalize_webdav_prefix() {
    local value="$1"

    if [[ -z "$value" ]]; then
        value="/dav"
    fi
    [[ "$value" != *[[:cntrl:]]* ]] || fail "webdav.prefix 不能包含控制字符: $value"
    [[ "$value" != *\\* && "$value" != *\?* && "$value" != *#* ]] || fail "webdav.prefix 不能包含反斜杠、? 或 #: $value"
    [[ "$value" == /* ]] || value="/$value"
    while [[ "$value" != "/" && "$value" == */ ]]; do
        value="${value%/}"
    done
    printf '%s\n' "$value"
}

toml_value() {
    local section="$1"
    local key="$2"
    local file="$3"

    [[ -f "$file" ]] || return 0

    if command -v python3 >/dev/null 2>&1; then
        local value
        if value=$(python3 - "$file" "$section" "$key" <<'PY'
import sys

try:
    import tomllib
except Exception:
    sys.exit(2)

path, section, key = sys.argv[1], sys.argv[2], sys.argv[3]
try:
    with open(path, "rb") as handle:
        data = tomllib.load(handle)
except Exception:
    sys.exit(2)

current = data
for part in section.split("."):
    if not isinstance(current, dict):
        sys.exit(0)
    current = current.get(part)
    if current is None:
        sys.exit(0)

if not isinstance(current, dict) or key not in current:
    sys.exit(0)

value = current[key]
if isinstance(value, bool):
    sys.stdout.write("true" if value else "false")
elif isinstance(value, (str, int, float)):
    sys.stdout.write(str(value))
elif hasattr(value, "isoformat"):
    sys.stdout.write(value.isoformat())
PY
        ); then
            printf '%s' "$value"
            return 0
        fi
    fi

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

find_nasd_binary() {
    local candidate

    if [[ -n "${MNEMONAS_NASD_BIN:-}" ]]; then
        [[ -x "$MNEMONAS_NASD_BIN" && ! -d "$MNEMONAS_NASD_BIN" ]] || return 1
        printf '%s\n' "$MNEMONAS_NASD_BIN"
        return 0
    fi

    if command -v nasd >/dev/null 2>&1; then
        command -v nasd
        return 0
    fi

    for candidate in "$SCRIPT_DIR/nasd" "$SCRIPT_DIR/../nasd"; do
        if [[ -x "$candidate" && ! -d "$candidate" ]]; then
            printf '%s\n' "$candidate"
            return 0
        fi
    done

    return 1
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

    DOMAIN="$(normalize_domain "$DOMAIN")" || fail "域名格式不安全: $DOMAIN"
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

resolve_webdav_prefix() {
    local configured_prefix=""

    if [[ -f "$CONFIG_PATH" ]]; then
        configured_prefix="$(toml_value webdav prefix "$CONFIG_PATH" || true)"
    fi
    WEBDAV_PREFIX="$(normalize_webdav_prefix "$configured_prefix")"
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
        function normalized_section(text) {
            sub("^\\[[[:space:]]*", "[", text)
            sub("[[:space:]]*\\]$", "]", text)
            gsub("[[:space:]]*\\.[[:space:]]*", ".", text)
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
                if (normalized_section(trimmed) == "[server]") {
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
    local nasd_bin=""

    if [[ "$CONFIGURE_MNEMONAS" != "1" ]]; then
        MNEMONAS_CONFIG_STATUS="skipped"
        log_warn "已跳过 MnemoNAS 配置修改；请手动设置 server.host 和 trusted_proxy_hops"
        return
    fi

    if [[ ! -f "$CONFIG_PATH" ]]; then
        MNEMONAS_CONFIG_STATUS="missing"
        log_warn "未找到 MnemoNAS 配置: $CONFIG_PATH"
        log_warn "请手动加入: [server] host = \"$UPSTREAM_HOST\", trusted_proxy_hops = $TRUSTED_PROXY_HOPS"
        return
    fi

    backup="${CONFIG_PATH}.bak.$(date +%Y%m%d%H%M%S)"
    cp "$CONFIG_PATH" "$backup"
    log_info "已备份 MnemoNAS 配置: $backup"

    update_mnemonas_server_config "$CONFIG_PATH" "$UPSTREAM_HOST" "$TRUSTED_PROXY_HOPS"
    MNEMONAS_CONFIG_STATUS="updated"
    log_info "已设置 MnemoNAS 后端监听: $UPSTREAM_ENDPOINT"
    log_info "已设置 server.trusted_proxy_hops = $TRUSTED_PROXY_HOPS"

    nasd_bin="$(find_nasd_binary || true)"
    if [[ -n "$nasd_bin" ]]; then
        check_log="$(mktemp)"
        if "$nasd_bin" --check-config --config "$CONFIG_PATH" >"$check_log" 2>&1; then
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
        restart_systemd_service mnemonas.service
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
        if { cat "$current_cron"; echo "$line"; } | crontab -; then
            log_info "已写入证书续期 cron"
        else
            rm -f -- "$current_cron"
            return 1
        fi
    fi
    rm -f -- "$current_cron"
}

configure_certbot_renewal() {
    local renew_line="0 3 * * * certbot renew --quiet --post-hook 'systemctl reload nginx'"

    if systemctl list-unit-files certbot.timer >/dev/null 2>&1; then
        if systemctl enable --now certbot.timer >/dev/null; then
            log_info "已启用 certbot.timer 自动续期"
            return 0
        fi
        log_warn "certbot.timer 启用失败，尝试写入 cron 续期任务"
    fi

    if command -v crontab >/dev/null 2>&1; then
        if install_cron_line_once "$renew_line"; then
            return 0
        fi
        log_warn "写入证书续期 cron 失败；请手动配置 certbot renew 自动续期"
        return 0
    fi

    log_warn "未检测到 certbot.timer 或 crontab；请手动配置 certbot renew 自动续期"
}

restart_systemd_service() {
    local service="$1"

    if systemctl restart "$service"; then
        return 0
    fi

    log_error "重启 $service 失败；请运行: systemctl status $service --no-pager；journalctl -u $service -n 100 --no-pager"
    return 1
}

reload_systemd_service() {
    local service="$1"

    if systemctl reload "$service"; then
        return 0
    fi

    log_error "重新加载 $service 失败；请运行: systemctl status $service --no-pager；journalctl -u $service -n 100 --no-pager"
    return 1
}

enable_systemd_service() {
    local service="$1"

    if systemctl enable "$service"; then
        return 0
    fi

    log_error "启用 $service 失败；请运行: systemctl status $service --no-pager；journalctl -u $service -n 100 --no-pager"
    return 1
}

write_caddy_config_file() {
    local target="$1"
    local target_dir tmp backup

    target_dir="$(dirname "$target")"
    mkdir -p "$target_dir"
    tmp="$(mktemp "$target_dir/.Caddyfile.new.XXXXXXXX")"

    cat > "$tmp" << EOF
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

    if ! caddy validate --config "$tmp"; then
        rm -f -- "$tmp"
        return 1
    fi

    if [[ -f "$target" ]]; then
        backup="${target}.bak.$(date +%Y%m%d%H%M%S)"
        cp "$target" "$backup"
        log_info "已备份 Caddyfile: $backup"
    fi

    mv -- "$tmp" "$target"
}

activate_caddy_config_file() {
    local target="$1"
    local target_dir target_backup
    local had_target=0

    target_dir="$(dirname "$target")"
    mkdir -p "$target_dir"
    target_backup="$(mktemp "$target_dir/.Caddyfile.activate.old.XXXXXXXX")"

    if [[ -f "$target" ]]; then
        cp -p "$target" "$target_backup"
        had_target=1
    fi

    if ! write_caddy_config_file "$target"; then
        rm -f -- "$target_backup"
        return 1
    fi

    if ! enable_systemd_service caddy; then
        restore_optional_file "$target" "$target_backup" "$had_target"
        rm -f -- "$target_backup"
        log_warn "Caddy 启用失败，已恢复先前配置"
        return 1
    fi

    if ! restart_systemd_service caddy; then
        restore_optional_file "$target" "$target_backup" "$had_target"
        rm -f -- "$target_backup"
        log_warn "Caddy 启动失败，已恢复先前配置"
        return 1
    fi

    rm -f -- "$target_backup"
}

render_nginx_config_file() {
    local target="$1"
    local domain_escaped upstream_escaped generated_escaped

    cat > "$target" << 'EOF'
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
    sed -i "s/DOMAIN_PLACEHOLDER/$domain_escaped/g" "$target"
    sed -i "s/UPSTREAM_PLACEHOLDER/$upstream_escaped/g" "$target"
    sed -i "s/GENERATED_TIME/$generated_escaped/g" "$target"
}

restore_nginx_config_file() {
    local target="$1"
    local target_backup="$2"
    local had_target="$3"
    local enabled_link="$4"
    local old_link_target="$5"
    local had_link="$6"

    if [[ "$had_target" == "1" ]]; then
        cp "$target_backup" "$target"
    else
        rm -f -- "$target"
    fi

    if [[ -n "$enabled_link" ]]; then
        if [[ "$had_link" == "1" ]]; then
            ln -sf "$old_link_target" "$enabled_link"
        else
            rm -f -- "$enabled_link"
        fi
    fi
}

render_nginx_challenge_config_file() {
    local target="$1"

    cat > "$target" << EOF
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
}

cleanup_nginx_backup_file() {
    local backup="$1"

    [[ -z "$backup" ]] || rm -f -- "$backup"
}

write_nginx_config_file() {
    local target="$1"
    local enabled_link="${2:-}"
    local target_dir target_backup old_link_target
    local had_target=0
    local had_link=0

    target_dir="$(dirname "$target")"
    mkdir -p "$target_dir"

    if [[ -f "$target" ]]; then
        target_backup="$(mktemp "$target_dir/.nginx-site.old.XXXXXXXX")"
        cp "$target" "$target_backup"
        had_target=1
    fi

    if [[ -n "$enabled_link" && -L "$enabled_link" ]]; then
        old_link_target="$(readlink "$enabled_link")"
        had_link=1
    fi

    render_nginx_config_file "$target"

    if [[ -n "$enabled_link" ]]; then
        ln -sf "$target" "$enabled_link"
    fi

    if ! nginx -t; then
        restore_nginx_config_file "$target" "${target_backup:-}" "$had_target" "$enabled_link" "${old_link_target:-}" "$had_link"
        cleanup_nginx_backup_file "${target_backup:-}"
        log_warn "Nginx 配置校验失败，已恢复先前配置"
        return 1
    fi

    cleanup_nginx_backup_file "${target_backup:-}"
}

activate_nginx_challenge_config_file() {
    local target="$1"
    local enabled_link="$2"
    local target_dir target_backup old_link_target
    local had_target=0
    local had_link=0

    target_dir="$(dirname "$target")"
    mkdir -p "$target_dir"

    if [[ -f "$target" ]]; then
        target_backup="$(mktemp "$target_dir/.nginx-challenge.old.XXXXXXXX")"
        cp "$target" "$target_backup"
        had_target=1
    fi

    if [[ -L "$enabled_link" ]]; then
        old_link_target="$(readlink "$enabled_link")"
        had_link=1
    fi

    render_nginx_challenge_config_file "$target"
    ln -sf "$target" "$enabled_link"

    if ! nginx -t; then
        restore_nginx_config_file "$target" "${target_backup:-}" "$had_target" "$enabled_link" "${old_link_target:-}" "$had_link"
        cleanup_nginx_backup_file "${target_backup:-}"
        log_warn "Nginx 临时配置校验失败，已恢复先前配置"
        return 1
    fi

    if ! enable_systemd_service nginx; then
        restore_nginx_config_file "$target" "${target_backup:-}" "$had_target" "$enabled_link" "${old_link_target:-}" "$had_link"
        cleanup_nginx_backup_file "${target_backup:-}"
        log_warn "Nginx 临时配置启用失败，已恢复先前配置"
        return 1
    fi

    if ! restart_systemd_service nginx; then
        restore_nginx_config_file "$target" "${target_backup:-}" "$had_target" "$enabled_link" "${old_link_target:-}" "$had_link"
        cleanup_nginx_backup_file "${target_backup:-}"
        log_warn "Nginx 临时配置启动失败，已恢复先前配置"
        return 1
    fi

    cleanup_nginx_backup_file "${target_backup:-}"
}

request_nginx_certificate() {
    local temp_config="$1"
    local enabled_link="$2"
    local temp_backup="$3"
    local had_temp="$4"
    local old_link_target="$5"
    local had_link="$6"

    if certbot certonly --webroot -w /var/www/certbot \
        -d "$DOMAIN" \
        --email "$EMAIL" \
        --agree-tos \
        --non-interactive; then
        cleanup_nginx_backup_file "$temp_backup"
        return 0
    fi

    restore_nginx_config_file "$temp_config" "$temp_backup" "$had_temp" "$enabled_link" "$old_link_target" "$had_link"
    cleanup_nginx_backup_file "$temp_backup"
    log_warn "Let's Encrypt 证书申请失败，已恢复先前 Nginx 配置"
    return 1
}

restore_optional_file() {
    local target="$1"
    local backup="$2"
    local had_target="$3"

    if [[ "$had_target" == "1" ]]; then
        cp -p "$backup" "$target"
    else
        rm -f -- "$target"
    fi
}

install_caddy_repo_files() {
    local key_path="${1:-/usr/share/keyrings/caddy-stable-archive-keyring.gpg}"
    local list_path="${2:-/etc/apt/sources.list.d/caddy-stable.list}"
    local key_dir list_dir key_stage_dir list_stage_dir
    local key_tmp list_tmp key_backup list_backup
    local key_had=0
    local list_had=0

    key_dir="$(dirname "$key_path")"
    list_dir="$(dirname "$list_path")"
    mkdir -p "$key_dir" "$list_dir"
    key_stage_dir="$(mktemp -d "$key_dir/.caddy-key.XXXXXXXX")"
    list_stage_dir="$(mktemp -d "$list_dir/.caddy-list.XXXXXXXX")"
    key_tmp="$key_stage_dir/key.gpg"
    list_tmp="$list_stage_dir/caddy.list"
    key_backup="$key_stage_dir/key.backup"
    list_backup="$list_stage_dir/list.backup"

    if ! curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o "$key_tmp"; then
        rm -rf -- "$key_stage_dir" "$list_stage_dir"
        return 1
    fi

    if ! curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' > "$list_tmp"; then
        rm -rf -- "$key_stage_dir" "$list_stage_dir"
        return 1
    fi

    if [[ -f "$key_path" ]]; then
        cp -p "$key_path" "$key_backup"
        key_had=1
    fi
    if [[ -f "$list_path" ]]; then
        cp -p "$list_path" "$list_backup"
        list_had=1
    fi

    if ! mv -- "$key_tmp" "$key_path"; then
        rm -rf -- "$key_stage_dir" "$list_stage_dir"
        return 1
    fi

    if ! mv -- "$list_tmp" "$list_path"; then
        restore_optional_file "$key_path" "$key_backup" "$key_had"
        restore_optional_file "$list_path" "$list_backup" "$list_had"
        rm -rf -- "$key_stage_dir" "$list_stage_dir"
        return 1
    fi

    rm -rf -- "$key_stage_dir" "$list_stage_dir"
}

deny_ufw_port() {
    local port="$1"
    local comment="$2"

    ufw --force delete allow "$port/tcp" >/dev/null 2>&1 || true
    run_ufw "限制 $comment" deny "$port/tcp" comment "$comment"
}

run_ufw() {
    local description="$1"
    shift

    if ufw "$@"; then
        return 0
    fi

    fail "$description 失败；请手动检查 UFW 或云安全组规则"
}

install_caddy() {
    log_info "安装 Caddy..."

    run_apt_get "更新 apt 索引" update
    run_apt_get "安装 Caddy 仓库依赖" install -y debian-keyring debian-archive-keyring apt-transport-https curl gnupg

    install_caddy_repo_files

    run_apt_get "更新 Caddy apt 索引" update
    run_apt_get "安装 Caddy" install -y caddy

    mkdir -p /var/log/caddy
    chown caddy:caddy /var/log/caddy

    log_info "配置并验证 Caddyfile..."
    activate_caddy_config_file /etc/caddy/Caddyfile

    log_info "Caddy 配置完成"
}

install_nginx() {
    local nginx_config nginx_temp_config nginx_enabled
    local rollback_temp_backup rollback_old_link_target
    local rollback_had_temp=0
    local rollback_had_link=0

    log_info "安装 Nginx 和 Certbot..."
    run_apt_get "更新 apt 索引" update
    run_apt_get "安装 Nginx 和 Certbot" install -y nginx certbot python3-certbot-nginx

    log_info "配置 Nginx..."

    nginx_config="/etc/nginx/sites-available/$DOMAIN"
    nginx_temp_config="/etc/nginx/sites-available/$DOMAIN.temp"
    nginx_enabled="/etc/nginx/sites-enabled/$DOMAIN"

    mkdir -p /var/www/certbot
    rm -f -- /etc/nginx/sites-enabled/default

    if [[ -f "$nginx_temp_config" ]]; then
        rollback_temp_backup="$(mktemp "$(dirname "$nginx_temp_config")/.nginx-challenge.pre-cert.old.XXXXXXXX")"
        cp "$nginx_temp_config" "$rollback_temp_backup"
        rollback_had_temp=1
    fi

    if [[ -L "$nginx_enabled" ]]; then
        rollback_old_link_target="$(readlink "$nginx_enabled")"
        rollback_had_link=1
    fi

    if ! activate_nginx_challenge_config_file "$nginx_temp_config" "$nginx_enabled"; then
        cleanup_nginx_backup_file "${rollback_temp_backup:-}"
        return 1
    fi

    log_info "申请 Let's Encrypt 证书..."
    request_nginx_certificate "$nginx_temp_config" "$nginx_enabled" "${rollback_temp_backup:-}" "$rollback_had_temp" "${rollback_old_link_target:-}" "$rollback_had_link"

    write_nginx_config_file "$nginx_config" "$nginx_enabled"
    rm -f -- "$nginx_temp_config"
    reload_systemd_service nginx

    log_info "配置证书自动续期..."
    configure_certbot_renewal

    log_info "Nginx + Certbot 配置完成"
}

configure_firewall() {
    if [[ "$CONFIGURE_FIREWALL" != "1" ]]; then
        FIREWALL_STATUS="skipped"
        log_warn "已跳过防火墙配置；请确认公网安全组只开放 80/443"
        return
    fi

    log_info "配置本机 UFW 防火墙规则..."
    if command -v ufw >/dev/null 2>&1; then
        run_ufw "允许 HTTP 入口" allow 80/tcp
        run_ufw "允许 HTTPS 入口" allow 443/tcp
        if [[ "$UPSTREAM_PORT" != "80" && "$UPSTREAM_PORT" != "443" ]]; then
            deny_ufw_port "$UPSTREAM_PORT" "MnemoNAS direct HTTP"
        fi
        deny_ufw_port "$DATAPLANE_GRPC_PORT" "MnemoNAS dataplane gRPC"
        deny_ufw_port "$DATAPLANE_HTTP_PORT" "MnemoNAS dataplane HTTP"
        FIREWALL_STATUS="updated"
        log_info "已允许 80/443，并限制 $UPSTREAM_PORT/$DATAPLANE_GRPC_PORT/$DATAPLANE_HTTP_PORT"
    else
        FIREWALL_STATUS="unavailable"
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

is_ipv4_loopback_host() {
    local host="$1"
    local octet
    local -a octets

    [[ "$host" =~ ^127\.([0-9]{1,3}\.){2}[0-9]{1,3}$ ]] || return 1
    IFS='.' read -r -a octets <<< "$host"
    for octet in "${octets[@]}"; do
        [[ ${#octet} -le 3 ]] || return 1
        (( 10#$octet >= 0 && 10#$octet <= 255 )) || return 1
    done
    return 0
}

is_loopback_host() {
    local host="$1"

    case "$host" in
        localhost|ip6-localhost|::1)
            return 0
            ;;
    esac
    is_ipv4_loopback_host "$host"
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
    local webdav_url webdav_probe_url

    webdav_url="https://$DOMAIN$WEBDAV_PREFIX"
    if [[ "$WEBDAV_PREFIX" == "/" ]]; then
        webdav_url="https://$DOMAIN/"
        webdav_probe_url="$webdav_url"
    else
        webdav_probe_url="$webdav_url/"
    fi

    echo ""
    log_info "=========================================="
    log_info "公网 HTTPS 入口配置完成"
    log_info "=========================================="
    echo ""
    echo "访问地址: https://$DOMAIN"
    echo "WebDAV:   $webdav_url"
    echo "后端入口: http://$UPSTREAM_ENDPOINT"
    echo ""
    echo "配置状态:"
    echo "  - 反向代理: $PROXY_TYPE"
    case "$MNEMONAS_CONFIG_STATUS" in
        updated)
            echo "  - MnemoNAS 配置: 已设置 server.host = \"$UPSTREAM_HOST\", trusted_proxy_hops = $TRUSTED_PROXY_HOPS"
            ;;
        skipped)
            echo "  - MnemoNAS 配置: 已跳过；请手动设置 server.host = \"$UPSTREAM_HOST\", trusted_proxy_hops = $TRUSTED_PROXY_HOPS"
            ;;
        missing)
            echo "  - MnemoNAS 配置: 未找到 $CONFIG_PATH；请手动设置 server.host = \"$UPSTREAM_HOST\", trusted_proxy_hops = $TRUSTED_PROXY_HOPS"
            ;;
        *)
            echo "  - MnemoNAS 配置: 未确认；请检查 server.host = \"$UPSTREAM_HOST\", trusted_proxy_hops = $TRUSTED_PROXY_HOPS"
            ;;
    esac
    case "$FIREWALL_STATUS" in
        updated)
            echo "  - 本机防火墙: 已允许 80/443，并限制直接访问 $UPSTREAM_PORT/$DATAPLANE_GRPC_PORT/$DATAPLANE_HTTP_PORT"
            ;;
        skipped)
            echo "  - 本机防火墙: 已跳过；请确认公网安全组或本机防火墙只开放 80/443"
            ;;
        unavailable)
            echo "  - 本机防火墙: 未检测到 ufw；请确认公网安全组或系统防火墙只开放 80/443，并限制 $UPSTREAM_PORT/$DATAPLANE_GRPC_PORT/$DATAPLANE_HTTP_PORT"
            ;;
        *)
            echo "  - 本机防火墙: 未确认；请确认只开放 80/443，并限制 $UPSTREAM_PORT/$DATAPLANE_GRPC_PORT/$DATAPLANE_HTTP_PORT"
            ;;
    esac
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
    echo "  curl -u \"\$WEBDAV_USER:\$WEBDAV_PASS\" -X PROPFIND $webdav_probe_url -H 'Depth: 0'"
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
    local tmp config list_config old_path fake_bin fake_ufw_log status enabled_link old_enabled_target
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

    config="$tmp/spaced-server.toml"
    cat > "$config" <<'EOF'
[ server ]
host = "0.0.0.0"
port = 8080

[ server . tls ]
enabled = false
EOF
    update_mnemonas_server_config "$config" "127.0.0.1" "1"
    grep -Fq '[ server ]' "$config" || fail "self-test failed: spaced server section was not preserved"
    grep -Fq 'host = "127.0.0.1"' "$config" || fail "self-test failed: spaced server host was not updated"
    grep -Fq 'trusted_proxy_hops = 1' "$config" || fail "self-test failed: spaced server trusted_proxy_hops was not inserted"
    [[ "$(grep -Fc '[server]' "$config")" == "0" ]] || fail "self-test failed: duplicate server section was appended"

    config="$tmp/missing-server.toml"
    cat > "$config" <<'EOF'
[storage]
root = "/srv/mnemonas"
EOF
    update_mnemonas_server_config "$config" "127.0.0.1" "1"
    grep -Fq '[server]' "$config" || fail "self-test failed: server section was not appended"
    grep -Fq 'host = "127.0.0.1"' "$config" || fail "self-test failed: appended host missing"

    [[ "$(normalize_webdav_prefix '')" == "/dav" ]] || fail "self-test failed: default WebDAV prefix formatting"
    [[ "$(normalize_webdav_prefix 'files')" == "/files" ]] || fail "self-test failed: relative WebDAV prefix formatting"
    [[ "$(normalize_webdav_prefix '/files/')" == "/files" ]] || fail "self-test failed: trailing slash WebDAV prefix formatting"
    [[ "$(normalize_domain 'NAS.EXAMPLE.COM.')" == "nas.example.com" ]] || fail "self-test failed: FQDN domain normalization"

    config="$tmp/custom-webdav-prefix.toml"
    cat > "$config" <<'EOF'
[webdav]
prefix = "/files\u002f"
EOF
    CONFIG_PATH="$config"
    WEBDAV_PREFIX=""
    resolve_webdav_prefix
    [[ "$WEBDAV_PREFIX" == "/files" ]] || fail "self-test failed: configured WebDAV prefix was not resolved"

    DOMAIN="nas.example.com"
    PROXY_TYPE="caddy"
    UPSTREAM_HOST="127.0.0.1"
    UPSTREAM_PORT="8080"
    UPSTREAM_ENDPOINT="127.0.0.1:8080"
    DATAPLANE_GRPC_PORT="9090"
    DATAPLANE_HTTP_PORT="9091"
    WEBDAV_PREFIX="/files"
    CONFIGURE_MNEMONAS=0
    CONFIGURE_FIREWALL=0
    MNEMONAS_CONFIG_STATUS="skipped"
    FIREWALL_STATUS="skipped"
    print_summary > "$tmp/summary-skipped.log"
    grep -Fq 'WebDAV:   https://nas.example.com/files' "$tmp/summary-skipped.log" || fail "self-test failed: summary WebDAV prefix missing"
    grep -Fq 'MnemoNAS 配置: 已跳过' "$tmp/summary-skipped.log" || fail "self-test failed: skipped MnemoNAS config summary missing"
    grep -Fq '本机防火墙: 已跳过' "$tmp/summary-skipped.log" || fail "self-test failed: skipped firewall summary missing"
    ! grep -Fq 'MnemoNAS 配置: server.host =' "$tmp/summary-skipped.log" || fail "self-test failed: skipped summary claimed MnemoNAS config was changed"

    fake_bin="$tmp/fake-bin"
    fake_ufw_log="$tmp/ufw.log"
    mkdir -p "$fake_bin"

    cat > "$fake_bin/ufw" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$MNEMONAS_FAKE_UFW_LOG"
if [[ "${1:-}" == "allow" && "${2:-}" == "443/tcp" ]]; then
    printf 'simulated ufw allow failure\n' >&2
    exit 22
fi
exit 0
EOF
    chmod +x "$fake_bin/ufw"
    CONFIGURE_FIREWALL=1
    UPSTREAM_PORT="18080"
    DATAPLANE_GRPC_PORT="19090"
    DATAPLANE_HTTP_PORT="19091"
    FIREWALL_STATUS="pending"
    set +e
    # shellcheck disable=SC2030,SC2031 # The fake PATH is intentionally scoped to this subshell.
    (PATH="$fake_bin:$PATH"; MNEMONAS_FAKE_UFW_LOG="$fake_ufw_log" configure_firewall) > "$tmp/firewall-failure.log" 2>&1
    status=$?
    set -e
    [[ "$status" -ne 0 ]] || fail "self-test failed: firewall setup accepted a failed UFW command"
    grep -Fq 'simulated ufw allow failure' "$tmp/firewall-failure.log" || fail "self-test failed: firewall setup hid UFW output"
    grep -Fq '允许 HTTPS 入口 失败' "$tmp/firewall-failure.log" || fail "self-test failed: firewall setup omitted failed phase"

    : > "$fake_ufw_log"
    cat > "$fake_bin/ufw" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$MNEMONAS_FAKE_UFW_LOG"
EOF
    chmod +x "$fake_bin/ufw"
    CONFIGURE_FIREWALL=1
    UPSTREAM_PORT="18080"
    DATAPLANE_GRPC_PORT="19090"
    DATAPLANE_HTTP_PORT="19091"
    FIREWALL_STATUS="pending"
    # shellcheck disable=SC2030,SC2031 # The fake PATH is intentionally scoped to this subshell.
    (PATH="$fake_bin:$PATH"; MNEMONAS_FAKE_UFW_LOG="$fake_ufw_log" configure_firewall) > "$tmp/firewall.log"
    grep -Fxq -- 'allow 80/tcp' "$fake_ufw_log" || fail "self-test failed: firewall did not allow HTTP"
    grep -Fxq -- 'allow 443/tcp' "$fake_ufw_log" || fail "self-test failed: firewall did not allow HTTPS"
    grep -Fxq -- '--force delete allow 18080/tcp' "$fake_ufw_log" || fail "self-test failed: firewall did not remove broad direct backend allow"
    grep -Fxq -- '--force delete allow 19090/tcp' "$fake_ufw_log" || fail "self-test failed: firewall did not remove broad dataplane gRPC allow"
    grep -Fxq -- '--force delete allow 19091/tcp' "$fake_ufw_log" || fail "self-test failed: firewall did not remove broad dataplane HTTP allow"
    grep -Fxq -- 'deny 18080/tcp comment MnemoNAS direct HTTP' "$fake_ufw_log" || fail "self-test failed: firewall did not deny direct backend"
    grep -Fxq -- 'deny 19090/tcp comment MnemoNAS dataplane gRPC' "$fake_ufw_log" || fail "self-test failed: firewall did not deny dataplane gRPC"
    grep -Fxq -- 'deny 19091/tcp comment MnemoNAS dataplane HTTP' "$fake_ufw_log" || fail "self-test failed: firewall did not deny dataplane HTTP"

    cat > "$fake_bin/systemctl" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "restart" ]]; then
    printf 'simulated restart failure\n' >&2
    exit 7
fi
exit 0
EOF
    chmod +x "$fake_bin/systemctl"
    set +e
    # shellcheck disable=SC2030,SC2031 # The fake PATH is intentionally scoped to this subshell.
    (PATH="$fake_bin:$PATH"; restart_systemd_service mnemonas.service) > "$tmp/restart-failure.log" 2>&1
    status=$?
    set -e
    [[ "$status" -ne 0 ]] || fail "self-test failed: restart helper accepted a failed service restart"
    grep -Fq 'simulated restart failure' "$tmp/restart-failure.log" || fail "self-test failed: restart helper hid systemctl output"
    grep -Fq '重启 mnemonas.service 失败' "$tmp/restart-failure.log" || fail "self-test failed: restart helper did not name the failed service"
    grep -Fq 'systemctl status mnemonas.service --no-pager' "$tmp/restart-failure.log" || fail "self-test failed: restart helper omitted status command"
    grep -Fq 'journalctl -u mnemonas.service -n 100 --no-pager' "$tmp/restart-failure.log" || fail "self-test failed: restart helper omitted journal command"

    cat > "$fake_bin/systemctl" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "enable" ]]; then
    printf 'simulated enable failure\n' >&2
    exit 17
fi
exit 0
EOF
    chmod +x "$fake_bin/systemctl"
    set +e
    # shellcheck disable=SC2030,SC2031 # The fake PATH is intentionally scoped to this subshell.
    (PATH="$fake_bin:$PATH"; enable_systemd_service caddy) > "$tmp/enable-failure.log" 2>&1
    status=$?
    set -e
    [[ "$status" -ne 0 ]] || fail "self-test failed: enable helper accepted a failed service enable"
    grep -Fq 'simulated enable failure' "$tmp/enable-failure.log" || fail "self-test failed: enable helper hid systemctl output"
    grep -Fq '启用 caddy 失败' "$tmp/enable-failure.log" || fail "self-test failed: enable helper did not name the failed service"
    grep -Fq 'systemctl status caddy --no-pager' "$tmp/enable-failure.log" || fail "self-test failed: enable helper omitted status command"
    grep -Fq 'journalctl -u caddy -n 100 --no-pager' "$tmp/enable-failure.log" || fail "self-test failed: enable helper omitted journal command"

    cat > "$fake_bin/caddy" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "validate" ]]; then
    printf 'simulated caddy validation failure\n' >&2
    exit 8
fi
exit 0
EOF
    chmod +x "$fake_bin/caddy"
    config="$tmp/Caddyfile"
    printf 'old caddyfile\n' > "$config"
    set +e
    # shellcheck disable=SC2030,SC2031 # The fake PATH is intentionally scoped to this subshell.
    (PATH="$fake_bin:$PATH"; write_caddy_config_file "$config") > "$tmp/caddy-validation-failure.log" 2>&1
    status=$?
    set -e
    [[ "$status" -ne 0 ]] || fail "self-test failed: Caddyfile writer accepted invalid generated config"
    grep -Fq 'simulated caddy validation failure' "$tmp/caddy-validation-failure.log" || fail "self-test failed: Caddyfile writer hid validation output"
    grep -Fq 'old caddyfile' "$config" || fail "self-test failed: Caddyfile writer did not preserve old config on validation failure"

    cat > "$fake_bin/caddy" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
    cat > "$fake_bin/systemctl" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "enable" ]]; then
    printf 'simulated caddy enable failure\n' >&2
    exit 17
fi
exit 0
EOF
    chmod +x "$fake_bin/caddy" "$fake_bin/systemctl"
    config="$tmp/Caddyfile"
    printf 'old caddyfile\n' > "$config"
    set +e
    # shellcheck disable=SC2030,SC2031 # The fake PATH is intentionally scoped to this subshell.
    (PATH="$fake_bin:$PATH"; activate_caddy_config_file "$config") > "$tmp/caddy-enable-failure.log" 2>&1
    status=$?
    set -e
    [[ "$status" -ne 0 ]] || fail "self-test failed: Caddy config activation accepted a failed service enable"
    grep -Fq 'simulated caddy enable failure' "$tmp/caddy-enable-failure.log" || fail "self-test failed: Caddy config activation hid enable output"
    grep -Fq '启用 caddy 失败' "$tmp/caddy-enable-failure.log" || fail "self-test failed: Caddy config activation omitted enable diagnostic"
    grep -Fq 'Caddy 启用失败，已恢复先前配置' "$tmp/caddy-enable-failure.log" || fail "self-test failed: Caddy config activation omitted enable rollback warning"
    grep -Fxq 'old caddyfile' "$config" || fail "self-test failed: Caddy config activation did not restore old config on enable failure"

    cat > "$fake_bin/caddy" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
    cat > "$fake_bin/systemctl" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "restart" ]]; then
    printf 'simulated caddy restart failure\n' >&2
    exit 18
fi
exit 0
EOF
    chmod +x "$fake_bin/caddy" "$fake_bin/systemctl"
    config="$tmp/Caddyfile"
    printf 'old caddyfile\n' > "$config"
    set +e
    # shellcheck disable=SC2030,SC2031 # The fake PATH is intentionally scoped to this subshell.
    (PATH="$fake_bin:$PATH"; activate_caddy_config_file "$config") > "$tmp/caddy-restart-failure.log" 2>&1
    status=$?
    set -e
    [[ "$status" -ne 0 ]] || fail "self-test failed: Caddy config activation accepted a failed service restart"
    grep -Fq 'simulated caddy restart failure' "$tmp/caddy-restart-failure.log" || fail "self-test failed: Caddy config activation hid restart output"
    grep -Fq '重启 caddy 失败' "$tmp/caddy-restart-failure.log" || fail "self-test failed: Caddy config activation omitted restart diagnostic"
    grep -Fq 'Caddy 启动失败，已恢复先前配置' "$tmp/caddy-restart-failure.log" || fail "self-test failed: Caddy config activation omitted rollback warning"
    grep -Fxq 'old caddyfile' "$config" || fail "self-test failed: Caddy config activation did not restore old config on restart failure"

    cat > "$fake_bin/nginx" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "-t" ]]; then
    printf 'simulated nginx validation failure\n' >&2
    exit 9
fi
exit 0
EOF
    chmod +x "$fake_bin/nginx"
    config="$tmp/nginx-site.conf"
    enabled_link="$tmp/nginx-enabled"
    old_enabled_target="$tmp/nginx-old-enabled.conf"
    printf 'old nginx config\n' > "$config"
    printf 'old enabled config\n' > "$old_enabled_target"
    ln -s "$old_enabled_target" "$enabled_link"
    set +e
    # shellcheck disable=SC2030,SC2031 # The fake PATH is intentionally scoped to this subshell.
    (PATH="$fake_bin:$PATH"; write_nginx_config_file "$config" "$enabled_link") > "$tmp/nginx-validation-failure.log" 2>&1
    status=$?
    set -e
    [[ "$status" -ne 0 ]] || fail "self-test failed: Nginx writer accepted invalid generated config"
    grep -Fq 'simulated nginx validation failure' "$tmp/nginx-validation-failure.log" || fail "self-test failed: Nginx writer hid validation output"
    grep -Fq 'old nginx config' "$config" || fail "self-test failed: Nginx writer did not preserve old config on validation failure"
    [[ "$(readlink "$enabled_link")" == "$old_enabled_target" ]] || fail "self-test failed: Nginx writer did not restore the enabled site link on validation failure"

    config="$tmp/nginx-challenge.conf"
    enabled_link="$tmp/nginx-challenge-enabled"
    old_enabled_target="$tmp/nginx-challenge-old-enabled.conf"
    printf 'old challenge enabled config\n' > "$old_enabled_target"
    ln -sf "$old_enabled_target" "$enabled_link"
    set +e
    # shellcheck disable=SC2030,SC2031 # The fake PATH is intentionally scoped to this subshell.
    (PATH="$fake_bin:$PATH"; activate_nginx_challenge_config_file "$config" "$enabled_link") > "$tmp/nginx-challenge-validation-failure.log" 2>&1
    status=$?
    set -e
    [[ "$status" -ne 0 ]] || fail "self-test failed: Nginx challenge config accepted invalid generated config"
    grep -Fq 'simulated nginx validation failure' "$tmp/nginx-challenge-validation-failure.log" || fail "self-test failed: Nginx challenge config hid validation output"
    [[ "$(readlink "$enabled_link")" == "$old_enabled_target" ]] || fail "self-test failed: Nginx challenge config did not restore the enabled site link on validation failure"

    cat > "$fake_bin/nginx" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
    chmod +x "$fake_bin/nginx"
    cat > "$fake_bin/systemctl" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "restart" ]]; then
    printf 'simulated restart failure\n' >&2
    exit 7
fi
exit 0
EOF
    chmod +x "$fake_bin/systemctl"
    config="$tmp/nginx-challenge-restart.conf"
    enabled_link="$tmp/nginx-challenge-restart-enabled"
    old_enabled_target="$tmp/nginx-challenge-restart-old-enabled.conf"
    printf 'old challenge restart enabled config\n' > "$old_enabled_target"
    ln -sf "$old_enabled_target" "$enabled_link"
    set +e
    # shellcheck disable=SC2030,SC2031 # The fake PATH is intentionally scoped to this subshell.
    (PATH="$fake_bin:$PATH"; activate_nginx_challenge_config_file "$config" "$enabled_link") > "$tmp/nginx-challenge-restart-failure.log" 2>&1
    status=$?
    set -e
    [[ "$status" -ne 0 ]] || fail "self-test failed: Nginx challenge config accepted a failed service restart"
    grep -Fq 'simulated restart failure' "$tmp/nginx-challenge-restart-failure.log" || fail "self-test failed: Nginx challenge config hid restart output"
    grep -Fq '重启 nginx 失败' "$tmp/nginx-challenge-restart-failure.log" || fail "self-test failed: Nginx challenge config omitted restart diagnostic"
    [[ "$(readlink "$enabled_link")" == "$old_enabled_target" ]] || fail "self-test failed: Nginx challenge config did not restore the enabled site link on restart failure"

    cat > "$fake_bin/certbot" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "certonly" ]]; then
    printf 'simulated certbot failure\n' >&2
    exit 10
fi
exit 0
EOF
    chmod +x "$fake_bin/certbot"
    config="$tmp/nginx-certbot-temp.conf"
    enabled_link="$tmp/nginx-certbot-enabled"
    old_enabled_target="$tmp/nginx-certbot-old-enabled.conf"
    printf 'challenge config\n' > "$config"
    printf 'old certbot enabled config\n' > "$old_enabled_target"
    ln -sf "$config" "$enabled_link"
    set +e
    # shellcheck disable=SC2030,SC2031 # The fake PATH is intentionally scoped to this subshell.
    (PATH="$fake_bin:$PATH"; request_nginx_certificate "$config" "$enabled_link" "" "0" "$old_enabled_target" "1") > "$tmp/nginx-certbot-failure.log" 2>&1
    status=$?
    set -e
    [[ "$status" -ne 0 ]] || fail "self-test failed: Nginx certificate request accepted a failed certbot run"
    grep -Fq 'simulated certbot failure' "$tmp/nginx-certbot-failure.log" || fail "self-test failed: Nginx certificate request hid certbot output"
    [[ "$(readlink "$enabled_link")" == "$old_enabled_target" ]] || fail "self-test failed: Nginx certificate request did not restore the enabled site link on certbot failure"
    [[ ! -e "$config" ]] || fail "self-test failed: Nginx certificate request did not remove the temporary challenge config on certbot failure"

    cat > "$fake_bin/systemctl" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "reload" ]]; then
    printf 'simulated reload failure\n' >&2
    exit 11
fi
exit 0
EOF
    chmod +x "$fake_bin/systemctl"
    set +e
    # shellcheck disable=SC2030,SC2031 # The fake PATH is intentionally scoped to this subshell.
    (PATH="$fake_bin:$PATH"; reload_systemd_service nginx) > "$tmp/reload-failure.log" 2>&1
    status=$?
    set -e
    [[ "$status" -ne 0 ]] || fail "self-test failed: reload helper accepted a failed service reload"
    grep -Fq 'simulated reload failure' "$tmp/reload-failure.log" || fail "self-test failed: reload helper hid systemctl output"
    grep -Fq '重新加载 nginx 失败' "$tmp/reload-failure.log" || fail "self-test failed: reload helper did not name the failed service"
    grep -Fq 'systemctl status nginx --no-pager' "$tmp/reload-failure.log" || fail "self-test failed: reload helper omitted status command"
    grep -Fq 'journalctl -u nginx -n 100 --no-pager' "$tmp/reload-failure.log" || fail "self-test failed: reload helper omitted journal command"

    cat > "$fake_bin/systemctl" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "list-unit-files" && "${2:-}" == "certbot.timer" ]]; then
    exit 0
fi
if [[ "${1:-}" == "enable" && "${2:-}" == "--now" && "${3:-}" == "certbot.timer" ]]; then
    printf 'simulated timer enable failure\n' >&2
    exit 12
fi
exit 0
EOF
    cat > "$fake_bin/crontab" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "-l" ]]; then
    exit 1
fi
cat > "$MNEMONAS_FAKE_CRONTAB"
EOF
    chmod +x "$fake_bin/systemctl" "$fake_bin/crontab"
    set +e
    # shellcheck disable=SC2030,SC2031 # The fake PATH is intentionally scoped to this subshell.
    (PATH="$fake_bin:$PATH"; MNEMONAS_FAKE_CRONTAB="$tmp/crontab.out" configure_certbot_renewal) > "$tmp/renewal-timer-fallback.log" 2>&1
    status=$?
    set -e
    [[ "$status" -eq 0 ]] || fail "self-test failed: renewal setup failed instead of falling back from certbot.timer to cron"
    grep -Fq 'simulated timer enable failure' "$tmp/renewal-timer-fallback.log" || fail "self-test failed: renewal setup hid certbot.timer failure"
    grep -Fq '已写入证书续期 cron' "$tmp/renewal-timer-fallback.log" || fail "self-test failed: renewal setup did not report cron fallback"
    grep -Fq "certbot renew --quiet --post-hook 'systemctl reload nginx'" "$tmp/crontab.out" || fail "self-test failed: renewal setup did not write cron fallback"

    cat > "$fake_bin/systemctl" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "list-unit-files" && "${2:-}" == "certbot.timer" ]]; then
    exit 0
fi
if [[ "${1:-}" == "enable" && "${2:-}" == "--now" && "${3:-}" == "certbot.timer" ]]; then
    printf 'simulated timer enable failure\n' >&2
    exit 12
fi
exit 0
EOF
    cat > "$fake_bin/crontab" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "-l" ]]; then
    exit 1
fi
printf 'simulated crontab write failure\n' >&2
exit 13
EOF
    chmod +x "$fake_bin/systemctl" "$fake_bin/crontab"
    set +e
    # shellcheck disable=SC2030,SC2031 # The fake PATH is intentionally scoped to this subshell.
    (PATH="$fake_bin:$PATH"; configure_certbot_renewal) > "$tmp/renewal-no-fallback.log" 2>&1
    status=$?
    set -e
    [[ "$status" -eq 0 ]] || fail "self-test failed: renewal setup failed without a fallback scheduler"
    grep -Fq 'simulated timer enable failure' "$tmp/renewal-no-fallback.log" || fail "self-test failed: renewal setup hid certbot.timer failure without fallback"
    grep -Fq '请手动配置 certbot renew 自动续期' "$tmp/renewal-no-fallback.log" || fail "self-test failed: renewal setup did not give manual renewal guidance"

    cat > "$fake_bin/curl" <<'EOF'
#!/usr/bin/env bash
printf 'fake caddy repo payload\n'
EOF
    cat > "$fake_bin/gpg" <<'EOF'
#!/usr/bin/env bash
out=""
while [[ "$#" -gt 0 ]]; do
    if [[ "$1" == "-o" ]]; then
        shift
        out="${1:-}"
    fi
    shift || true
done
cat >/dev/null
printf 'partial key\n' > "$out"
printf 'simulated gpg failure\n' >&2
exit 14
EOF
    chmod +x "$fake_bin/curl" "$fake_bin/gpg"
    config="$tmp/caddy-stable-archive-keyring.gpg"
    list_config="$tmp/caddy-stable.list"
    printf 'old key\n' > "$config"
    printf 'old list\n' > "$list_config"
    set +e
    # shellcheck disable=SC2030,SC2031 # The fake PATH is intentionally scoped to this subshell.
    (PATH="$fake_bin:$PATH"; install_caddy_repo_files "$config" "$list_config") > "$tmp/caddy-repo-gpg-failure.log" 2>&1
    status=$?
    set -e
    [[ "$status" -ne 0 ]] || fail "self-test failed: Caddy repo setup accepted a failed key conversion"
    grep -Fq 'simulated gpg failure' "$tmp/caddy-repo-gpg-failure.log" || fail "self-test failed: Caddy repo setup hid gpg failure output"
    grep -Fxq 'old key' "$config" || fail "self-test failed: Caddy repo setup did not preserve old key on gpg failure"
    grep -Fxq 'old list' "$list_config" || fail "self-test failed: Caddy repo setup changed source list on gpg failure"

    cat > "$fake_bin/curl" <<'EOF'
#!/usr/bin/env bash
case "${*: -1}" in
    *debian.deb.txt)
        printf 'simulated source list download failure\n' >&2
        exit 15
        ;;
    *)
        printf 'fake caddy repo payload\n'
        ;;
esac
EOF
    cat > "$fake_bin/gpg" <<'EOF'
#!/usr/bin/env bash
out=""
while [[ "$#" -gt 0 ]]; do
    if [[ "$1" == "-o" ]]; then
        shift
        out="${1:-}"
    fi
    shift || true
done
cat >/dev/null
printf 'new key\n' > "$out"
EOF
    chmod +x "$fake_bin/curl" "$fake_bin/gpg"
    printf 'old key\n' > "$config"
    printf 'old list\n' > "$list_config"
    set +e
    # shellcheck disable=SC2030,SC2031 # The fake PATH is intentionally scoped to this subshell.
    (PATH="$fake_bin:$PATH"; install_caddy_repo_files "$config" "$list_config") > "$tmp/caddy-repo-source-failure.log" 2>&1
    status=$?
    set -e
    [[ "$status" -ne 0 ]] || fail "self-test failed: Caddy repo setup accepted a failed source list download"
    grep -Fq 'simulated source list download failure' "$tmp/caddy-repo-source-failure.log" || fail "self-test failed: Caddy repo setup hid source list failure output"
    grep -Fxq 'old key' "$config" || fail "self-test failed: Caddy repo setup did not preserve old key on source list failure"
    grep -Fxq 'old list' "$list_config" || fail "self-test failed: Caddy repo setup did not preserve old source list on source list failure"

    cat > "$fake_bin/apt-get" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" > "$MNEMONAS_FAKE_APT_LOG"
printf 'simulated apt failure\n' >&2
exit 16
EOF
    chmod +x "$fake_bin/apt-get"
    set +e
    # shellcheck disable=SC2030,SC2031 # The fake PATH is intentionally scoped to this subshell.
    (PATH="$fake_bin:$PATH"; MNEMONAS_FAKE_APT_LOG="$tmp/apt.log" run_apt_get "安装测试包" install -y caddy) > "$tmp/apt-failure.log" 2>&1
    status=$?
    set -e
    [[ "$status" -ne 0 ]] || fail "self-test failed: apt helper accepted a failed apt-get command"
    grep -Fq 'simulated apt failure' "$tmp/apt-failure.log" || fail "self-test failed: apt helper hid apt-get output"
    grep -Fq '安装测试包 失败' "$tmp/apt-failure.log" || fail "self-test failed: apt helper omitted failure context"
    grep -Fq 'DPkg::Lock::Timeout=120' "$tmp/apt.log" || fail "self-test failed: apt helper did not configure dpkg lock timeout"

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

    if [[ -x "$SCRIPT_DIR/../nasd" ]]; then
        local old_path
        # shellcheck disable=SC2031 # The previous PATH assignment was intentionally subshell-local.
        old_path="$PATH"
        # This intentionally hides PATH so the self-test proves adjacent release binary discovery.
        # shellcheck disable=SC2123
        PATH="$tmp/no-path"
        [[ "$(find_nasd_binary)" == "$SCRIPT_DIR/../nasd" ]] || fail "self-test failed: adjacent release nasd was not discovered"
        PATH="$old_path"
    fi

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
    resolve_webdav_prefix
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
