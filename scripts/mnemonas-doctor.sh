#!/usr/bin/env bash

set -u

SERVICE_USER="${SERVICE_USER:-mnemonas}"
BIN_DIR="${BIN_DIR:-/usr/local/bin}"
WEB_DIR="${WEB_DIR:-/usr/local/share/mnemonas/web}"
CONFIG_PATH="${CONFIG_PATH:-/etc/mnemonas/config.toml}"
SYSTEMD_DIR="${SYSTEMD_DIR:-/etc/systemd/system}"
BACKUP_ROOT="${BACKUP_ROOT:-/backup/mnemonas}"
MIN_FREE_BYTES="${MIN_FREE_BYTES:-10737418240}"
PUBLIC_DOMAIN="${MNEMONAS_PUBLIC_DOMAIN:-}"
PUBLIC_CERT_FAILURE=0
PROC_NET_TCP_PATH="${MNEMONAS_PROC_NET_TCP_PATH:-/proc/net/tcp}"
PROC_NET_TCP6_PATH="${MNEMONAS_PROC_NET_TCP6_PATH:-/proc/net/tcp6}"

FAILURES=0
WARNINGS=0
CONFIG_TOML_SYNTAX_VALID=unknown

ok() {
  printf '[OK] %s\n' "$*"
}

note() {
  printf '[INFO] %s\n' "$*"
}

warn() {
  WARNINGS=$((WARNINGS + 1))
  printf '[WARN] %s\n' "$*"
}

fail() {
  FAILURES=$((FAILURES + 1))
  printf '[FAIL] %s\n' "$*"
}

die() {
  printf '[FAIL] %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<EOF
Usage: mnemonas-doctor [--public-domain <domain>]

Options:
  --public-domain <domain>  Also verify the public HTTPS entry and direct-port exposure.
  -h, --help                Show this help.
EOF
}

have() {
  command -v "$1" >/dev/null 2>&1
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --public-domain)
        [[ $# -ge 2 ]] || die "--public-domain requires a domain"
        PUBLIC_DOMAIN="$2"
        shift 2
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        die "unknown argument: $1"
        ;;
    esac
  done
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

  awk -v section="[$section]" -v key="$key" -v dotted_key="$section.$key" '
    function trim(text) {
      gsub("^[[:space:]]+|[[:space:]]+$", "", text)
      return text
    }
    function normalize_key_name(text) {
      text = trim(text)
      gsub("[[:space:]]*\\.[[:space:]]*", ".", text)
      return text
    }
    function unquote_value(text) {
      text = trim(text)
      gsub("^\"|\"$", "", text)
      gsub("^\047|\047$", "", text)
      return text
    }
    function print_assignment_value(text, expected_key,    pos, lhs, value) {
      pos = index(text, "=")
      if (!pos) {
        return 0
      }
      lhs = normalize_key_name(substr(text, 1, pos - 1))
      if (lhs != expected_key) {
        return 0
      }
      value = unquote_value(substr(text, pos + 1))
      print value
      return 1
    }
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
      line = trim(line)
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
    !in_section && print_assignment_value(line, dotted_key) {
      exit
    }
    in_section && print_assignment_value(line, key) {
      exit
    }
  ' "$file"
}

toml_key_exists() {
  local section="$1"
  local key="$2"
  local file="$3"

  [[ -f "$file" ]] || return 1

  if command -v python3 >/dev/null 2>&1; then
    if python3 - "$file" "$section" "$key" <<'PY'
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
        sys.exit(1)
    current = current.get(part)
    if current is None:
        sys.exit(1)

sys.exit(0 if isinstance(current, dict) and key in current else 1)
PY
    then
      return 0
    else
      case "$?" in
        1) return 1 ;;
      esac
    fi
  fi

  awk -v section="[$section]" -v key="$key" -v dotted_key="$section.$key" '
    function trim(text) {
      gsub("^[[:space:]]+|[[:space:]]+$", "", text)
      return text
    }
    function normalize_key_name(text) {
      text = trim(text)
      gsub("[[:space:]]*\\.[[:space:]]*", ".", text)
      return text
    }
    function assignment_matches(text, expected_key,    pos, lhs) {
      pos = index(text, "=")
      if (!pos) {
        return 0
      }
      lhs = normalize_key_name(substr(text, 1, pos - 1))
      return lhs == expected_key
    }
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
      line = trim(line)
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
    !in_section && assignment_matches(line, dotted_key) {
      found = 1
      exit
    }
    in_section && assignment_matches(line, key) {
      found = 1
      exit
    }
    END {
      exit found ? 0 : 1
    }
  ' "$file"
}

expand_user_path() {
  local path="$1"

  case "$path" in
    "")
      printf '%s\n' ""
      ;;
    \~)
      if [[ -n "${HOME:-}" ]]; then
        printf '%s\n' "$HOME"
      else
        printf '%s\n' "$path"
      fi
      ;;
    \~/*)
      if [[ -n "${HOME:-}" ]]; then
        printf '%s/%s\n' "$HOME" "${path#\~/}"
      else
        printf '%s\n' "$path"
      fi
      ;;
    *)
      printf '%s\n' "$path"
      ;;
  esac
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

port_listening() {
  local port="$1"
  local_addresses_for_port "$port" | grep -q .
}

local_addresses_for_port() {
  local port="$1"

  if ss_available; then
    ss -lntH | awk -v suffix=":$port" '$4 ~ suffix "$" { print $4 }'
    return
  fi

  proc_net_local_addresses_for_port "$port"
}

ss_available() {
  [[ "${MNEMONAS_DOCTOR_DISABLE_SS:-0}" != "1" ]] && have ss
}

can_inspect_local_ports() {
  ss_available || [[ -r "$PROC_NET_TCP_PATH" || -r "$PROC_NET_TCP6_PATH" ]]
}

can_strictly_inspect_local_ports() {
  ss_available || [[ -r "$PROC_NET_TCP_PATH" && -r "$PROC_NET_TCP6_PATH" ]]
}

port_inspection_source() {
  if ss_available; then
    printf 'ss output\n'
  else
    printf 'local port table\n'
  fi
}

proc_net_local_addresses_for_port() {
  local port="$1"
  proc_net_tcp4_addresses_for_port "$port"
  proc_net_tcp6_addresses_for_port "$port"
}

proc_net_tcp4_addresses_for_port() {
  local port="$1"
  local want_port local_addr state hex_addr hex_port b1 b2 b3 b4

  [[ -r "$PROC_NET_TCP_PATH" ]] || return 0
  printf -v want_port '%04X' "$((10#$port))"

  while read -r _ local_addr _ state _; do
    [[ "$local_addr" == "local_address" ]] && continue
    [[ "$state" == "0A" ]] || continue
    hex_addr="${local_addr%%:*}"
    hex_port="${local_addr##*:}"
    [[ "${hex_port^^}" == "$want_port" ]] || continue
    [[ "$hex_addr" =~ ^[0-9A-Fa-f]{8}$ ]] || continue

    b1="${hex_addr:6:2}"
    b2="${hex_addr:4:2}"
    b3="${hex_addr:2:2}"
    b4="${hex_addr:0:2}"
    printf '%d.%d.%d.%d:%d\n' "$((16#$b1))" "$((16#$b2))" "$((16#$b3))" "$((16#$b4))" "$((16#$hex_port))"
  done < "$PROC_NET_TCP_PATH"
}

proc_net_tcp6_addresses_for_port() {
  local port="$1"
  local want_port local_addr state hex_addr hex_port host

  [[ -r "$PROC_NET_TCP6_PATH" ]] || return 0
  printf -v want_port '%04X' "$((10#$port))"

  while read -r _ local_addr _ state _; do
    [[ "$local_addr" == "local_address" ]] && continue
    [[ "$state" == "0A" ]] || continue
    hex_addr="${local_addr%%:*}"
    hex_port="${local_addr##*:}"
    [[ "${hex_port^^}" == "$want_port" ]] || continue
    [[ "$hex_addr" =~ ^[0-9A-Fa-f]{32}$ ]] || continue

    case "${hex_addr^^}" in
      00000000000000000000000000000000)
        host="::"
        ;;
      00000000000000000000000001000000)
        host="::1"
        ;;
      *)
        host="${hex_addr^^}"
        ;;
    esac
    printf '[%s]:%d\n' "$host" "$((16#$hex_port))"
  done < "$PROC_NET_TCP6_PATH"
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

  if ! can_inspect_local_ports; then
    warn "$label port $port cannot be inspected; install iproute2/ss or make /proc/net/tcp readable"
    return
  fi

  while IFS= read -r address; do
    [[ -n "$address" ]] || continue
    host="$(host_from_ss_local_address "$address" "$port")"
    if ! is_loopback_host "$host"; then
      unsafe_addresses+=("$address")
    fi
  done < <(local_addresses_for_port "$port")

  if [[ "${#unsafe_addresses[@]}" -eq 0 ]]; then
    ok "$label port $port is loopback-only"
  else
    warn "$label port $port is listening beyond loopback (${unsafe_addresses[*]}); keep dataplane off public and untrusted networks"
  fi
}

check_loopback_only_port_strict() {
  local port="$1"
  local label="$2"
  local address host
  local -a unsafe_addresses=()

  if ! can_strictly_inspect_local_ports; then
    fail "$label port $port cannot be fully inspected; install iproute2/ss or make both $PROC_NET_TCP_PATH and $PROC_NET_TCP6_PATH readable before public exposure"
    return
  fi

  while IFS= read -r address; do
    [[ -n "$address" ]] || continue
    host="$(host_from_ss_local_address "$address" "$port")"
    if ! is_loopback_host "$host"; then
      unsafe_addresses+=("$address")
    fi
  done < <(local_addresses_for_port "$port")

  if [[ "${#unsafe_addresses[@]}" -eq 0 ]]; then
    ok "$label port $port is loopback-only"
  else
    fail "$label port $port is listening beyond loopback (${unsafe_addresses[*]}); public deployments must expose only the HTTPS reverse proxy"
  fi
}

is_valid_tcp_host() {
  local host="$1"
  local label
  local -a labels

  host="${host%.}"
  [[ -n "$host" ]] || return 1
  [[ "$host" != *"["* && "$host" != *"]"* ]] || return 1

  if [[ "$host" == *:* ]]; then
    if have python3; then
      python3 - "$host" <<'PY'
import ipaddress
import sys

try:
    address = ipaddress.ip_address(sys.argv[1])
except ValueError:
    sys.exit(1)

sys.exit(0 if address.version == 6 else 1)
PY
      return
    fi
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

is_ipv4_like_host() {
  local host="$1"
  local octet
  local -a octets

  IFS='.' read -r -a octets <<< "$host"
  [[ "${#octets[@]}" -eq 4 ]] || return 1
  for octet in "${octets[@]}"; do
    [[ "$octet" =~ ^[0-9]+$ ]] || return 1
  done
  return 0
}

require_safe_tcp_port() {
  local value="$1"
  local label="$2"

  [[ -n "$value" ]] || die "$label cannot be empty"
  [[ "$value" != *[[:space:]]* ]] || die "$label cannot contain whitespace: $value"
  [[ "$value" != *[[:cntrl:]]* ]] || die "$label cannot contain control characters: $value"
  [[ "$value" =~ ^[0-9]+$ ]] || die "$label must be numeric: $value"
  (( 10#$value >= 1 && 10#$value <= 65535 )) || die "$label must be between 1 and 65535: $value"
}

normalize_tcp_port() {
  local value="$1"

  printf '%s\n' "$((10#$value))"
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

require_safe_tcp_addr() {
  local value="$1"
  local label="$2"
  local host=""
  local port=""

  [[ -n "$value" ]] || die "$label cannot be empty"
  [[ "$value" != *[[:space:]]* ]] || die "$label cannot contain whitespace: $value"
  [[ "$value" != *[[:cntrl:]]* ]] || die "$label cannot contain control characters: $value"

  if [[ "$value" =~ ^\[([^][]+)\]:([0-9]+)$ ]]; then
    host="${BASH_REMATCH[1]}"
    port="${BASH_REMATCH[2]}"
  elif [[ "$value" =~ ^([^:]+):([0-9]+)$ ]]; then
    host="${BASH_REMATCH[1]}"
    port="${BASH_REMATCH[2]}"
  else
    die "$label must be a host:port address: $value"
  fi

  is_valid_tcp_host "$host" || die "$label host is invalid: $value"
  require_safe_tcp_port "$port" "$label port"
}

require_safe_http_url() {
  local value="$1"
  local label="$2"

  [[ -n "$value" ]] || die "$label cannot be empty"
  [[ "$value" != *[[:space:]]* ]] || die "$label cannot contain whitespace: $value"
  [[ "$value" != *[[:cntrl:]]* ]] || die "$label cannot contain control characters: $value"
  [[ "$value" =~ ^https?://[^[:space:]]+$ ]] || die "$label must be an http(s) URL: $value"
}

require_safe_public_domain() {
  local value="$1"
  local normalized

  [[ -z "$value" ]] && return 0
  [[ "$value" != *[[:space:]]* ]] || die "PUBLIC_DOMAIN cannot contain whitespace: $value"
  [[ "$value" != *[[:cntrl:]]* ]] || die "PUBLIC_DOMAIN cannot contain control characters: $value"
  [[ "$value" != *"/"* && "$value" != *":"* ]] || die "PUBLIC_DOMAIN must be a hostname without scheme or port: $value"
  is_valid_tcp_host "$value" || die "PUBLIC_DOMAIN is invalid: $value"
  normalized="${value%.}"
  normalized="${normalized,,}"
  [[ "$normalized" == *.* ]] || die "PUBLIC_DOMAIN must be a fully qualified hostname: $value"
  [[ "$normalized" != "localhost" && "$normalized" != *.localhost ]] || die "PUBLIC_DOMAIN must not be localhost: $value"
  ! is_ipv4_like_host "$normalized" || die "PUBLIC_DOMAIN must be a hostname, not an IP address: $value"
}

normalize_public_domain() {
  local value="${1,,}"

  [[ -z "$value" ]] && return 0
  if [[ "$value" == *. ]]; then
    value="${value%.}"
    [[ "$value" != *. ]] || die "PUBLIC_DOMAIN is invalid: $1"
  fi
  printf '%s\n' "$value"
}

is_true_value() {
  local value

  value="$(trim_ascii_whitespace "$1")"
  value="${value,,}"

  [[ "$value" == "true" || "$value" == "1" || "$value" == "yes" || "$value" == "on" ]]
}

is_false_value() {
  local value

  value="$(trim_ascii_whitespace "$1")"
  value="${value,,}"

  [[ "$value" == "false" || "$value" == "0" || "$value" == "no" || "$value" == "off" ]]
}

normalize_webdav_auth_type_for_public_check() {
  local value

  value="$(trim_ascii_whitespace "$1")"
  value="${value,,}"
  if [[ -z "$value" ]]; then
    value="basic"
  fi
  printf '%s\n' "$value"
}

webdav_basic_password_risk() {
  local value lower

  value="$(trim_ascii_whitespace "$1")"
  [[ -n "$value" ]] || return 1
  lower="${value,,}"
  case "$lower" in
    admin|changeme|change-me|change_this_password|change-this-password|change-this-strong-password|change-this-webdav-password|mnemonas|password|password123|very-strong-password-here|webdav|webdav-password|webdavpassword|*change-this*)
      printf 'placeholder\n'
      return 0
      ;;
  esac
  if (( ${#value} < 16 )); then
    printf 'too_short\n'
    return 0
  fi
  return 1
}

json_string_value() {
  local file="$1"
  local key="$2"

  python3 - "$file" "$key" <<'PY'
import json
import sys

path, key = sys.argv[1], sys.argv[2]
try:
    with open(path, "r", encoding="utf-8") as handle:
        data = json.load(handle)
except Exception:
    sys.exit(2)

if not isinstance(data, dict):
    sys.exit(3)
value = data.get(key)
if not isinstance(value, str):
    sys.exit(3)
sys.stdout.write(value)
PY
}

parse_args "$@"
require_safe_public_domain "$PUBLIC_DOMAIN"
PUBLIC_DOMAIN="$(normalize_public_domain "$PUBLIC_DOMAIN")"

configured_storage_root=""
configured_server_port=""
configured_grpc_address=""
configured_http_address=""
configured_server_host=""
configured_trusted_proxy_hops=""
configured_auth_enabled=""
configured_auth_users_file=""
configured_auth_access_token_ttl=""
configured_auth_access_token_ttl_set=0
configured_auth_refresh_token_ttl=""
configured_auth_refresh_token_ttl_set=0
configured_webdav_enabled=""
configured_webdav_auth_type=""
configured_webdav_prefix=""
configured_webdav_prefix_set=0
configured_webdav_password=""
configured_allow_unsafe_no_auth=""
configured_share_enabled=""
configured_share_base_url=""
configured_share_default_expires_in=""
configured_share_default_expires_in_set=0
configured_share_default_max_access=""
if [[ -f "$CONFIG_PATH" ]]; then
  configured_server_host="$(toml_value server host "$CONFIG_PATH")"
  configured_storage_root="$(toml_value storage root "$CONFIG_PATH")"
  configured_server_port="$(toml_value server port "$CONFIG_PATH")"
  configured_trusted_proxy_hops="$(toml_value server trusted_proxy_hops "$CONFIG_PATH")"
  configured_grpc_address="$(toml_value dataplane grpc_address "$CONFIG_PATH")"
  configured_auth_enabled="$(toml_value auth enabled "$CONFIG_PATH")"
  configured_auth_users_file="$(toml_value auth users_file "$CONFIG_PATH")"
  configured_auth_access_token_ttl="$(toml_value auth access_token_ttl "$CONFIG_PATH")"
  configured_auth_refresh_token_ttl="$(toml_value auth refresh_token_ttl "$CONFIG_PATH")"
  if toml_key_exists auth access_token_ttl "$CONFIG_PATH"; then
    configured_auth_access_token_ttl_set=1
  fi
  if toml_key_exists auth refresh_token_ttl "$CONFIG_PATH"; then
    configured_auth_refresh_token_ttl_set=1
  fi
  configured_webdav_enabled="$(toml_value webdav enabled "$CONFIG_PATH")"
  configured_webdav_auth_type="$(toml_value webdav auth_type "$CONFIG_PATH")"
  configured_webdav_prefix="$(toml_value webdav prefix "$CONFIG_PATH")"
  configured_webdav_password="$(toml_value webdav password "$CONFIG_PATH")"
  if toml_key_exists webdav prefix "$CONFIG_PATH"; then
    configured_webdav_prefix_set=1
  fi
  configured_allow_unsafe_no_auth="$(toml_value security allow_unsafe_no_auth "$CONFIG_PATH")"
  configured_share_enabled="$(toml_value share enabled "$CONFIG_PATH")"
  configured_share_base_url="$(toml_value share base_url "$CONFIG_PATH")"
  configured_share_default_expires_in="$(toml_value share default_expires_in "$CONFIG_PATH")"
  configured_share_default_max_access="$(toml_value share default_max_access "$CONFIG_PATH")"
  if toml_key_exists share default_expires_in "$CONFIG_PATH"; then
    configured_share_default_expires_in_set=1
  fi
fi
configured_http_address="$(systemd_env_value DATAPLANE_HTTP_ADDR "$SYSTEMD_DIR/mnemonas-dataplane.service")"

STORAGE_ROOT="${STORAGE_ROOT:-${configured_storage_root:-/srv/mnemonas}}"
STORAGE_ROOT="$(expand_user_path "$STORAGE_ROOT")"
configured_auth_users_file="$(expand_user_path "$configured_auth_users_file")"
SERVER_PORT="${SERVER_PORT:-${configured_server_port:-8080}}"
DATAPLANE_GRPC_ADDR="${DATAPLANE_GRPC_ADDR:-${configured_grpc_address:-127.0.0.1:9090}}"
DATAPLANE_HTTP_ADDR="${DATAPLANE_HTTP_ADDR:-${configured_http_address:-127.0.0.1:9091}}"
DATAPLANE_GRPC_PORT="${DATAPLANE_GRPC_PORT:-$(tcp_addr_port "$DATAPLANE_GRPC_ADDR" || true)}"
DATAPLANE_HTTP_PORT="${DATAPLANE_HTTP_PORT:-$(tcp_addr_port "$DATAPLANE_HTTP_ADDR" || true)}"

require_safe_tcp_port "$SERVER_PORT" "SERVER_PORT"
require_safe_tcp_addr "$DATAPLANE_GRPC_ADDR" "DATAPLANE_GRPC_ADDR"
require_safe_tcp_addr "$DATAPLANE_HTTP_ADDR" "DATAPLANE_HTTP_ADDR"
require_safe_tcp_port "$DATAPLANE_GRPC_PORT" "DATAPLANE_GRPC_PORT"
require_safe_tcp_port "$DATAPLANE_HTTP_PORT" "DATAPLANE_HTTP_PORT"
SERVER_PORT="$(normalize_tcp_port "$SERVER_PORT")"
DATAPLANE_GRPC_PORT="$(normalize_tcp_port "$DATAPLANE_GRPC_PORT")"
DATAPLANE_HTTP_PORT="$(normalize_tcp_port "$DATAPLANE_HTTP_PORT")"
SERVER_URL="${SERVER_URL:-http://127.0.0.1:$SERVER_PORT}"
DATAPLANE_URL="${DATAPLANE_URL:-http://127.0.0.1:$DATAPLANE_HTTP_PORT}"
require_safe_http_url "$SERVER_URL" "SERVER_URL"
require_safe_http_url "$DATAPLANE_URL" "DATAPLANE_URL"

check_file() {
  local path="$1"
  local label="$2"
  if [[ -f "$path" ]]; then
    ok "$label exists: $path"
  else
    fail "$label missing: $path"
  fi
}

check_executable_file() {
  local path="$1"
  local label="$2"

  if [[ ! -f "$path" ]]; then
    fail "$label missing: $path"
    return
  fi
  if [[ -x "$path" ]]; then
    ok "$label is executable: $path"
  else
    fail "$label is not executable: $path"
  fi
}

check_dir() {
  local path="$1"
  local label="$2"
  if [[ -d "$path" ]]; then
    ok "$label exists: $path"
  else
    fail "$label missing: $path"
  fi
}

check_service_user_writable_dir() {
  local path="$1"
  local label="$2"

  if [[ ! -d "$path" ]]; then
    return
  fi

  if have runuser && have getent && [[ "$(id -u)" -eq 0 ]] && getent passwd "$SERVICE_USER" >/dev/null 2>&1; then
    if runuser -u "$SERVICE_USER" -- test -w "$path"; then
      ok "$label is writable by $SERVICE_USER: $path"
    else
      fail "$label is not writable by $SERVICE_USER: $path"
    fi
    return
  fi

  if [[ -w "$path" ]]; then
    ok "$label is writable by current user: $path"
  else
    warn "$label is not writable by current user and runuser is unavailable: $path"
  fi
}

check_no_other_access() {
  local path="$1"
  local label="$2"

  [[ -d "$path" ]] || return
  if ! have stat; then
    return
  fi

  local mode mode_tail other
  mode="$(stat -c '%a' "$path" 2>/dev/null || true)"
  [[ -n "$mode" ]] || return
  mode_tail="${mode: -3}"
  other="${mode_tail:2:1}"
  if (( 10#$other == 0 )); then
    ok "$label does not allow other-user access: $path"
  else
    warn "$label allows other-user access (mode $mode); consider chmod o-rwx $path"
  fi
}

check_private_dir_mode() {
  local path="$1"
  local label="$2"

  [[ -d "$path" ]] || return
  if ! have stat; then
    return
  fi

  local mode mode_tail group other
  mode="$(stat -c '%a' "$path" 2>/dev/null || true)"
  [[ -n "$mode" ]] || return
  mode_tail="${mode: -3}"
  group="${mode_tail:1:1}"
  other="${mode_tail:2:1}"
  if (( 10#$group == 0 && 10#$other == 0 )); then
    ok "$label is private to its owner: $path"
  else
    warn "$label is not private (mode $mode); consider chmod 700 $path"
  fi
}

check_private_file_mode() {
  local path="$1"
  local label="$2"

  [[ -f "$path" ]] || return
  if ! have stat; then
    return
  fi

  local mode mode_tail group other
  mode="$(stat -c '%a' "$path" 2>/dev/null || true)"
  [[ -n "$mode" ]] || return
  mode_tail="${mode: -3}"
  group="${mode_tail:1:1}"
  other="${mode_tail:2:1}"
  if (( 10#$group == 0 && 10#$other == 0 )); then
    ok "$label is private to its owner: $path"
  else
    warn "$label is not private (mode $mode); consider chmod 600 $path"
  fi
}

first_symlink_path_component() {
  local path="$1"
  local include_leaf="${2:-0}"
  local current
  local remaining
  local part
  local -a parts
  local last_index
  local i

  [[ -n "$path" ]] || return 1
  while [[ "$path" != "/" && "$path" == */ ]]; do
    path="${path%/}"
  done

  if [[ "$path" == /* ]]; then
    current="/"
    remaining="${path#/}"
  else
    current="."
    remaining="$path"
  fi

  IFS='/' read -r -a parts <<< "$remaining"
  last_index=$((${#parts[@]} - 1))
  if [[ "$include_leaf" != "1" ]]; then
    last_index=$((last_index - 1))
  fi
  (( last_index >= 0 )) || return 1

  for ((i = 0; i <= last_index; i++)); do
    part="${parts[$i]}"
    [[ -n "$part" && "$part" != "." ]] || continue
    if [[ "$current" == "/" ]]; then
      current="/$part"
    elif [[ "$current" == "." ]]; then
      current="$part"
    else
      current="$current/$part"
    fi
    if [[ -L "$current" ]]; then
      printf '%s\n' "$current"
      return 0
    fi
  done

  return 1
}

check_sensitive_dir_path() {
  local path="$1"
  local label="$2"
  local symlink_component

  if [[ -L "$path" ]]; then
    warn "$label path is a symlink; use a regular private directory: $path"
  elif symlink_component="$(first_symlink_path_component "$path")"; then
    warn "$label path contains a symlink component; use a regular private directory path: $symlink_component in $path"
  elif [[ ! -e "$path" ]]; then
    warn "$label missing: $path"
  elif [[ ! -d "$path" ]]; then
    warn "$label is not a directory: $path"
  else
    check_private_dir_mode "$path" "$label"
  fi
}

check_sensitive_file_path() {
  local path="$1"
  local label="$2"
  local missing_message="${3:-}"
  local symlink_component

  if [[ -L "$path" ]]; then
    warn "$label path is a symlink; use a regular private file: $path"
  elif symlink_component="$(first_symlink_path_component "$path")"; then
    warn "$label path contains a symlink component; use a regular private file path: $symlink_component in $path"
  elif [[ ! -e "$path" ]]; then
    if [[ -n "$missing_message" ]]; then
      warn "$missing_message: $path"
    fi
  elif [[ ! -f "$path" ]]; then
    warn "$label is not a regular file: $path"
  else
    check_private_file_mode "$path" "$label"
  fi
}

check_config_file() {
  local path="$1"
  local symlink_component

  if [[ -L "$path" ]]; then
    warn "config file path is a symlink; use a regular private file: $path"
  elif symlink_component="$(first_symlink_path_component "$path")"; then
    warn "config file path contains a symlink component; use a regular private config path: $symlink_component in $path"
  elif [[ ! -e "$path" ]]; then
    fail "config missing: $path"
  elif [[ ! -f "$path" ]]; then
    fail "config is not a regular file: $path"
  else
    check_private_file_mode "$path" "config file"
  fi
}

check_config_toml_syntax() {
  local path="$1"
  local parse_out
  local status

  [[ -f "$path" ]] || return

  if ! have python3; then
    warn "python3 not available; skipping config TOML syntax check"
    return
  fi

  parse_out="$(mktemp -t mnemonas-doctor-config-toml.XXXXXX)"
  if python3 - "$path" >"$parse_out" 2>&1 <<'PY'
import sys

try:
    import tomllib
except Exception:
    print("python tomllib is unavailable", file=sys.stderr)
    sys.exit(3)

path = sys.argv[1]
try:
    with open(path, "rb") as handle:
        tomllib.load(handle)
except Exception as exc:
    print(str(exc), file=sys.stderr)
    sys.exit(2)
PY
  then
    CONFIG_TOML_SYNTAX_VALID=1
    ok "config TOML syntax is valid"
    rm -f -- "$parse_out"
    return
  fi

  status=$?
  if [[ "$status" -eq 3 ]]; then
    CONFIG_TOML_SYNTAX_VALID=unknown
    warn "python tomllib is unavailable; skipping config TOML syntax check"
  else
    CONFIG_TOML_SYNTAX_VALID=0
    fail "config TOML syntax is invalid: $path"
    sed 's/^/[INFO] TOML parse error: /' "$parse_out"
  fi
  rm -f -- "$parse_out"
}

check_http() {
  local url="$1"
  local label="$2"
  if have curl && curl -fsS "$url" >/dev/null 2>&1; then
    ok "$label reachable: $url"
  else
    fail "$label not reachable: $url"
  fi
}

check_http_unreachable() {
  local url="$1"
  local label="$2"
  local curl_result status http_code
  if ! have curl; then
    warn "curl not available; skipping $label exposure check"
    return
  fi

  curl_result="$(curl -sS --connect-timeout 3 --max-time 5 -o /dev/null -w '%{http_code}' "$url" 2>/dev/null)"
  status=$?
  http_code="${curl_result//$'\n'/}"
  if [[ "$status" -eq 0 && "$http_code" =~ ^[0-9][0-9][0-9]$ && "$http_code" != "000" ]]; then
    fail "$label is publicly reachable: $url (HTTP $http_code)"
  else
    ok "$label is not publicly reachable: $url"
  fi
}

https_redirect_targets_domain() {
  local domain="$1"
  local redirect_url="$2"

  case "$redirect_url" in
    "https://$domain"|"https://$domain/"*|"https://$domain?"*|"https://$domain#"*)
      return 0
      ;;
    "https://$domain:443"|"https://$domain:443/"*|"https://$domain:443?"*|"https://$domain:443#"*)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

check_http_redirects_to_https() {
  local domain="$1"
  local url="http://$domain/health"
  local curl_result status http_code redirect_url

  if ! have curl; then
    fail "curl is required for public HTTP-to-HTTPS redirect checks"
    return
  fi

  curl_result="$(curl -sS -I --connect-timeout 3 --max-time 5 -o /dev/null -w '%{http_code} %{redirect_url}' "$url" 2>/dev/null)"
  status=$?
  if [[ "$status" -ne 0 ]]; then
    fail "public HTTP redirect check was not reachable: $url"
    return
  fi

  http_code="${curl_result%% *}"
  redirect_url="${curl_result#* }"
  if [[ "$http_code" =~ ^30(1|2|3|7|8)$ ]] && https_redirect_targets_domain "$domain" "$redirect_url"; then
    ok "public HTTP redirects to HTTPS: $url -> $redirect_url"
  else
    fail "public HTTP does not clearly redirect to HTTPS: $url returned ${http_code:-unknown}"
  fi
}

response_header_has_token() {
  local headers_file="$1"
  local header_lc="${2,,}"
  local token_lc="${3,,}"

  awk -v header="$header_lc" -v token="$token_lc" '
    function trim(value) {
      gsub(/^[ \t]+|[ \t]+$/, "", value)
      return value
    }
    {
      sub(/\r$/, "", $0)
      idx = index($0, ":")
      if (idx < 1) {
        next
      }
      name = tolower(substr($0, 1, idx - 1))
      if (name != header) {
        next
      }
      value = substr($0, idx + 1)
      count = split(value, parts, ",")
      for (i = 1; i <= count; i++) {
        if (tolower(trim(parts[i])) == token) {
          found = 1
        }
      }
    }
    END {
      exit found ? 0 : 1
    }
  ' "$headers_file"
}

response_header_equals() {
  local headers_file="$1"
  local header_lc="${2,,}"
  local expected_lc="${3,,}"

  awk -v header="$header_lc" -v expected="$expected_lc" '
    function trim(value) {
      gsub(/^[ \t]+|[ \t]+$/, "", value)
      return value
    }
    {
      sub(/\r$/, "", $0)
      idx = index($0, ":")
      if (idx < 1) {
        next
      }
      name = tolower(substr($0, 1, idx - 1))
      if (name != header) {
        next
      }
      value = tolower(trim(substr($0, idx + 1)))
      if (value == expected) {
        found = 1
      }
    }
    END {
      exit found ? 0 : 1
    }
  ' "$headers_file"
}

response_header_exists() {
  local headers_file="$1"
  local header_lc="${2,,}"

  awk -v header="$header_lc" '
    {
      sub(/\r$/, "", $0)
      idx = index($0, ":")
      if (idx < 1) {
        next
      }
      name = tolower(substr($0, 1, idx - 1))
      if (name == header) {
        found = 1
      }
    }
    END {
      exit found ? 0 : 1
    }
  ' "$headers_file"
}

check_https_certificate() {
  local domain="$1"
  local cert_out cert_err status enddate

  if ! have openssl; then
    PUBLIC_CERT_FAILURE=1
    fail "openssl is required for public HTTPS certificate checks; install openssl so certificate hostname and expiry can be verified before public exposure"
    return
  fi

  cert_out="$(mktemp -t mnemonas-doctor-cert.XXXXXX)"
  cert_err="$(mktemp -t mnemonas-doctor-cert-err.XXXXXX)"

  if have timeout; then
    timeout 8 openssl s_client \
      -connect "$domain:443" \
      -servername "$domain" \
      -verify_hostname "$domain" \
      -verify_return_error \
      </dev/null >"$cert_out" 2>"$cert_err"
  else
    openssl s_client \
      -connect "$domain:443" \
      -servername "$domain" \
      -verify_hostname "$domain" \
      -verify_return_error \
      </dev/null >"$cert_out" 2>"$cert_err"
  fi
  status=$?

  if [[ "$status" -ne 0 ]]; then
    PUBLIC_CERT_FAILURE=1
    fail "public HTTPS certificate verification failed for $domain:443"
    rm -f "$cert_out" "$cert_err"
    return
  fi

  if grep -Eq 'Verify return code: 0 \(ok\)|Verification: OK' "$cert_out" "$cert_err"; then
    ok "public HTTPS certificate matches $domain"
  else
    PUBLIC_CERT_FAILURE=1
    fail "public HTTPS certificate verification did not report success for $domain"
  fi

  if openssl x509 -noout -checkend 2592000 -in "$cert_out" >/dev/null 2>&1; then
    enddate="$(openssl x509 -noout -enddate -in "$cert_out" 2>/dev/null | sed 's/^notAfter=//')"
    if [[ -n "$enddate" ]]; then
      ok "public HTTPS certificate is valid for at least 30 days (expires: $enddate)"
    else
      ok "public HTTPS certificate is valid for at least 30 days"
    fi
  else
    PUBLIC_CERT_FAILURE=1
    fail "public HTTPS certificate expires within 30 days or cannot be parsed"
  fi

  rm -f "$cert_out" "$cert_err"
}

check_certificate_renewal_automation() {
  local found=0

  if have certbot; then
    ok "certificate renewal tool detected: certbot; verify with: sudo certbot renew --dry-run"
    found=1
  fi

  if have caddy || (have systemctl && systemctl is-active --quiet caddy 2>/dev/null); then
    ok "certificate automation detected: Caddy; check renewal logs with: sudo journalctl -u caddy --since '24 hours ago'"
    found=1
  fi

  if have docker && docker ps --format '{{.Names}} {{.Image}}' 2>/dev/null | grep -Eiq 'traefik'; then
    ok "certificate automation detected: Traefik container; verify ACME storage and container logs before relying on renewal"
    found=1
  fi

  if [[ "$found" -eq 0 ]]; then
    warn "no local certificate renewal automation detected; if TLS is not managed by Cloudflare or another provider, configure Caddy, certbot timer, or Traefik ACME before public use"
  fi
}

print_certificate_failure_guidance() {
  local domain="$1"
  note "certificate failure triage for $domain: verify DNS A/AAAA records, cloud firewall 80/443, HTTP-01 challenge reachability, reverse-proxy logs, and then rerun: sudo mnemonas-doctor --public-domain $domain"
  if have certbot; then
    note "certbot triage: sudo certbot renew --dry-run; sudo journalctl -u certbot --since '24 hours ago'"
  fi
  if have caddy || (have systemctl && systemctl is-active --quiet caddy 2>/dev/null); then
    note "Caddy triage: sudo systemctl status caddy --no-pager; sudo journalctl -u caddy --since '24 hours ago'"
  fi
  if have docker; then
    note "Traefik triage: docker logs <traefik-container>; confirm acme.json is writable and persisted"
  fi
}

tcp_connectable() {
  local host="$1"
  local port="$2"

  if have timeout; then
    timeout 3 bash -c "</dev/tcp/$host/$port" >/dev/null 2>&1
    return $?
  fi

  return 2
}

check_tcp_unreachable() {
  local host="$1"
  local port="$2"
  local label="$3"
  local status=0

  tcp_connectable "$host" "$port"
  status=$?
  if [[ "$status" -eq 0 ]]; then
    fail "$label port $port is publicly reachable on $host"
  elif [[ "$status" -eq 2 ]]; then
    warn "timeout command not available; skipping $label public TCP exposure check"
  else
    ok "$label port $port is not publicly reachable on $host"
  fi
}

http_url_authority() {
  local value="$1"
  local without_scheme authority

  without_scheme="${value#*://}"
  authority="${without_scheme%%/*}"
  authority="${authority%%\\*}"
  authority="${authority%%\?*}"
  authority="${authority%%#*}"
  printf '%s\n' "$authority"
}

http_url_has_userinfo() {
  local authority

  authority="$(http_url_authority "$1")"
  [[ "$authority" == *@* ]]
}

http_url_has_query_or_fragment() {
  local without_scheme path_part

  without_scheme="${1#*://}"
  path_part="${without_scheme#*/}"
  [[ "$without_scheme" == */* && ( "$path_part" == *\?* || "$path_part" == *#* ) ]] && return 0
  [[ "$without_scheme" == *\?* || "$without_scheme" == *#* ]]
}

http_url_host() {
  local authority host_port

  authority="$(http_url_authority "$1")"
  host_port="${authority##*@}"
  if [[ "$host_port" =~ ^\[([^][]+)\](:[0-9]+)?$ ]]; then
    printf '%s\n' "${BASH_REMATCH[1]}"
    return
  fi
  if [[ "$host_port" =~ ^([^:]+):[0-9]+$ ]]; then
    printf '%s\n' "${BASH_REMATCH[1]}"
    return
  fi
  printf '%s\n' "$host_port"
}

redact_http_url_for_log() {
  local value="$1"
  local lower_value
  local scheme
  local authority
  local host_port
  local safe_authority
  local suffix=""

  value="$(trim_ascii_whitespace "$value")"
  lower_value="${value,,}"
  case "$lower_value" in
    http://*|https://*)
      ;;
    *)
      printf '<redacted-url>\n'
      return
      ;;
  esac

  scheme="${value%%://*}://"
  authority="$(http_url_authority "$value")"
  host_port="${authority##*@}"
  if [[ "$authority" == *@* ]]; then
    safe_authority="<redacted>@$host_port"
  else
    safe_authority="$host_port"
  fi
  [[ "$value" == *\?* ]] && suffix="${suffix}?<redacted>"
  [[ "$value" == *#* ]] && suffix="${suffix}#<redacted>"

  printf '%s%s%s\n' "$scheme" "$safe_authority" "$suffix"
}

http_url_port() {
  local authority host_port

  authority="$(http_url_authority "$1")"
  host_port="${authority##*@}"
  if [[ "$host_port" =~ ^\[[^][]+\]:([0-9]+)$ ]]; then
    printf '%s\n' "${BASH_REMATCH[1]}"
    return
  fi
  if [[ "$host_port" =~ ^[^:]+:([0-9]+)$ ]]; then
    printf '%s\n' "${BASH_REMATCH[1]}"
  fi
}

http_url_authority_has_invalid_host_port_syntax() {
  local authority host_port

  authority="$(http_url_authority "$1")"
  host_port="${authority##*@}"

  if [[ "$host_port" == \[* ]]; then
    [[ "$host_port" =~ ^\[[^][]+\](:[0-9]+)?$ ]] || return 0
    return 1
  fi

  if [[ "$host_port" == *:* ]]; then
    [[ "$host_port" =~ ^[^:]+:[0-9]+$ ]] || return 0
  fi
  return 1
}

http_url_path() {
  local value="$1"
  local without_scheme path_part path_start
  local char i

  without_scheme="${value#*://}"
  path_start=""
  for ((i = 0; i < ${#without_scheme}; i++)); do
    char="${without_scheme:i:1}"
    if [[ "$char" == "/" || "$char" == "\\" ]]; then
      path_start="${without_scheme:i}"
      break
    fi
    if [[ "$char" == "?" || "$char" == "#" ]]; then
      break
    fi
  done
  if [[ -z "$path_start" ]]; then
    printf '/\n'
    return
  fi
  path_part="${path_start%%\?*}"
  path_part="${path_part%%#*}"
  [[ -n "$path_part" ]] || path_part="/"
  printf '%s\n' "$path_part"
}

http_url_path_ends_with_share_route() {
  local path="$1"

  path="$(http_url_path_decode_slashes "$path")"
  while [[ "$path" != "/" && "$path" == */ ]]; do
    path="${path%/}"
  done
  [[ "$path" == "/s" || "$path" == */s ]]
}

http_url_path_has_duplicate_slashes() {
	local path="$1"

	path="$(http_url_path_decode_slashes "$path")"
	[[ "$path" == *"//"* ]]
}

http_url_path_has_dot_segments() {
  local path="$1"
  local segment
  local -a segments

  path="$(http_url_path_decode_slashes_and_dots "$path")"
  IFS='/' read -r -a segments <<< "$path"
  for segment in "${segments[@]}"; do
    [[ "$segment" == "." || "$segment" == ".." ]] && return 0
  done
  return 1
}

http_url_path_has_backslashes() {
	local path="$1"
	local path_lower

	path_lower="${path,,}"
	[[ "$path" == *\\* || "$path_lower" == *"%5c"* ]]
}

http_url_path_has_query_or_fragment_markers() {
  local path_lower

  path_lower="${1,,}"
  [[ "$path_lower" == *"%3f"* || "$path_lower" == *"%23"* ]]
}

http_url_path_decode_slashes() {
  local path="$1"

  path="${path//%2F//}"
	path="${path//%2f//}"
	printf '%s\n' "$path"
}

http_url_path_decode_slashes_and_dots() {
  local path

  path="$(http_url_path_decode_slashes "$1")"
  path="${path//%2E/.}"
  path="${path//%2e/.}"
  printf '%s\n' "$path"
}

http_url_host_is_valid() {
  local host="$1"

  [[ -n "$host" ]] || return 1
  if [[ "$host" == *. ]]; then
    host="${host%.}"
    [[ "$host" != *. ]] || return 1
  fi
  is_valid_tcp_host "$host"
}

trim_ascii_whitespace() {
  local value="$1"

  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s\n' "$value"
}

go_duration_to_nanoseconds() {
  local value="$1"

  have python3 || return 127
  python3 - "$value" <<'PY'
from decimal import Decimal, InvalidOperation
import re
import sys

value = sys.argv[1].strip()
if value == "":
    sys.exit(4)
if value == "0":
    print(0)
    sys.exit(0)
if value.startswith("-"):
    sys.exit(3)
if value.startswith("+"):
    value = value[1:]

unit_ns = {
    "ns": Decimal(1),
    "us": Decimal(1000),
    "ms": Decimal(1000000),
    "s": Decimal(1000000000),
    "m": Decimal(60 * 1000000000),
    "h": Decimal(60 * 60 * 1000000000),
}
pattern = re.compile(r"([0-9]+(?:\.[0-9]*)?|\.[0-9]+)(ns|us|ms|s|m|h)")
pos = 0
total = Decimal(0)
matched = False
while pos < len(value):
    match = pattern.match(value, pos)
    if not match:
        sys.exit(2)
    try:
        amount = Decimal(match.group(1))
    except InvalidOperation:
        sys.exit(2)
    total += amount * unit_ns[match.group(2)]
    pos = match.end()
    matched = True

if not matched or total < 0:
    sys.exit(2)
print(int(total))
PY
}

check_public_python_available() {
  if have python3; then
    ok "python3 is available for public diagnostics"
  else
    fail "python3 is required for public diagnostics; install python3 so duration, users.json, and generated WebDAV credential checks can be verified before public exposure"
  fi
}

check_public_getent_available() {
  if have getent; then
    ok "getent is available for public DNS diagnostics"
  else
    fail "getent is required for public diagnostics; install getent/libc-bin so public DNS A/AAAA resolution can be verified before public exposure"
  fi
}

check_public_curl_available() {
  if have curl; then
    ok "curl is available for public diagnostics"
  else
    fail "curl is required for public diagnostics; install curl so public HTTPS health, redirects, WebDAV/share probes, and direct exposure checks can be verified before public exposure"
  fi
}

check_public_dns_resolution() {
  local domain="$1"
  local resolved first_address

  have getent || return

  if resolved="$(getent ahosts "$domain" 2>/dev/null)" && [[ -n "$resolved" ]]; then
    first_address="$(printf '%s\n' "$resolved" | awk 'NF { print $1; exit }')"
    if [[ -n "$first_address" ]]; then
      ok "public domain resolves locally: $domain ($first_address)"
    else
      ok "public domain resolves locally: $domain"
    fi
  else
    fail "public domain does not resolve locally: $domain; fix DNS A/AAAA records before public exposure"
  fi
}

check_public_share_default_policy() {
  local raw_expires_in
  local expires_in
  local expires_ns
  local parse_status
  local max_access
  local recommended_ns

  recommended_ns=$((30 * 24 * 60 * 60 * 1000000000))
  max_access="$(trim_ascii_whitespace "${configured_share_default_max_access:-0}")"
  [[ -n "$max_access" ]] || max_access="0"
  if [[ ! "$max_access" =~ ^-?[0-9]+$ ]]; then
    fail "public share.default_max_access must be zero or greater: $max_access"
  elif [[ "$max_access" == -* ]]; then
    fail "public share.default_max_access must be zero or greater: $max_access"
  elif [[ "$max_access" =~ ^0+$ ]]; then
    warn "public share.default_max_access leaves new share links without an access limit; set a default such as 20"
  else
    ok "public share.default_max_access limits new share link accesses: $max_access"
  fi

  if [[ "$configured_share_default_expires_in_set" == "1" ]]; then
    raw_expires_in="${configured_share_default_expires_in:-}"
  else
    raw_expires_in="168h"
  fi
  expires_in="$(trim_ascii_whitespace "$raw_expires_in")"
  if [[ -z "$expires_in" || "$expires_in" == "0" ]]; then
    warn "public share.default_expires_in leaves new share links without an expiry; set a default such as 168h"
    return
  fi

  expires_ns="$(go_duration_to_nanoseconds "$expires_in")"
  parse_status=$?
  case "$parse_status" in
    0)
      ;;
    127)
      warn "python3 not available; skipping public share.default_expires_in duration check"
      return
      ;;
    3)
      fail "public share.default_expires_in must be empty, 0, or a non-negative duration: $expires_in"
      return
      ;;
    *)
      fail "public share.default_expires_in must be empty, 0, or a non-negative duration: $expires_in"
      return
      ;;
  esac

  if (( expires_ns == 0 )); then
    warn "public share.default_expires_in leaves new share links without an expiry; set a default such as 168h"
    return
  fi

  if (( expires_ns > recommended_ns )); then
    warn "public share.default_expires_in is longer than 720h: $expires_in"
  else
    ok "public share.default_expires_in is within 720h: $expires_in"
  fi
}

check_public_share_response_boundary() {
  local domain="$1"
  local probe_id="mnemonas-doctor-probe"
  local url="https://$domain/api/v1/public/shares/$probe_id"
  local headers_file
  local curl_result status http_code
  local enforce_json_headers=0
  local missing=()

  if ! have curl; then
    warn "curl not available; skipping public share response boundary check"
    return
  fi

  headers_file="$(mktemp -t mnemonas-doctor-share-headers.XXXXXX)" || {
    warn "could not create temporary header file; skipping public share response boundary check"
    return
  }

  curl_result="$(curl -sS --connect-timeout 3 --max-time 5 -o /dev/null -D "$headers_file" -w '%{http_code}' "$url" 2>/dev/null)"
  status=$?
  http_code="${curl_result//$'\n'/}"
  if [[ "$status" -ne 0 || ! "$http_code" =~ ^[0-9][0-9][0-9]$ || "$http_code" == "000" ]]; then
    fail "public share API probe was not readable: $url"
    rm -f "$headers_file"
    return
  fi

  case "$http_code" in
    404)
      ok "public share API probe reached MnemoNAS: $url (HTTP $http_code)"
      enforce_json_headers=1
      ;;
    410)
      fail "public share API reports sharing disabled despite share.enabled=true: $url (HTTP $http_code)"
      enforce_json_headers=1
      ;;
    2??)
      fail "public share API probe unexpectedly returned success for a reserved probe id: $url (HTTP $http_code)"
      enforce_json_headers=1
      ;;
    401|403)
      fail "public share API probe was blocked before MnemoNAS share lookup: $url (HTTP $http_code); route public share lookups to MnemoNAS before enabling public shares"
      ;;
    3??)
      fail "public share API probe redirected before MnemoNAS share lookup: $url (HTTP $http_code); route public share lookups to MnemoNAS before enabling public shares"
      ;;
    *)
      fail "public share API probe returned HTTP $http_code at $url; verify share routing before public use"
      ;;
  esac

  if (( enforce_json_headers == 0 )); then
    rm -f "$headers_file"
    return
  fi

  response_header_has_token "$headers_file" "Cache-Control" "private" || missing+=("Cache-Control=private")
  response_header_has_token "$headers_file" "Cache-Control" "no-cache" || missing+=("Cache-Control=no-cache")
  response_header_has_token "$headers_file" "Vary" "Cookie" || missing+=("Vary=Cookie")
  response_header_equals "$headers_file" "X-Content-Type-Options" "nosniff" || missing+=("X-Content-Type-Options=nosniff")
  response_header_equals "$headers_file" "Referrer-Policy" "no-referrer" || missing+=("Referrer-Policy=no-referrer")
  response_header_exists "$headers_file" "Set-Cookie" && missing+=("Set-Cookie must be absent on missing-share probes")

  if (( ${#missing[@]} > 0 )); then
    fail "public share JSON response is missing cache/security headers (${missing[*]}): $url (HTTP $http_code)"
  else
    ok "public share JSON responses use private cache and Cookie Vary boundaries: $url"
  fi

  rm -f "$headers_file"
}

effective_secrets_file() {
  printf '%s\n' "$STORAGE_ROOT/secrets.json"
}

check_public_webdav_generated_password() {
  local secrets_file
  local password
  local password_risk
  local parse_status
  local symlink_component

  secrets_file="$(effective_secrets_file)"
  if [[ -L "$secrets_file" ]]; then
    fail "public WebDAV generated password file is a symlink; use a regular private file: $secrets_file"
    return
  fi
  if symlink_component="$(first_symlink_path_component "$secrets_file")"; then
    fail "public WebDAV generated password file path contains a symlink component; use a regular private file path: $symlink_component in $secrets_file"
    return
  fi
  if [[ ! -e "$secrets_file" ]]; then
    fail "public WebDAV generated password file is missing; start MnemoNAS once or set webdav.password before public access: $secrets_file"
    return
  fi
  if [[ ! -f "$secrets_file" ]]; then
    fail "public WebDAV generated password path is not a regular file: $secrets_file"
    return
  fi

  check_private_file_mode "$secrets_file" "public WebDAV generated password file"
  if ! have python3; then
    warn "python3 not available; skipping public WebDAV generated password strength check"
    return
  fi

  password="$(json_string_value "$secrets_file" webdav_password)"
  parse_status=$?
  case "$parse_status" in
    0)
      ;;
    2)
      fail "public WebDAV generated password file could not be parsed; cannot verify Basic Auth password: $secrets_file"
      return
      ;;
    *)
      fail "public WebDAV generated password is missing from secrets.json; start MnemoNAS once or set webdav.password before public access"
      return
      ;;
  esac

  if [[ -z "$password" ]]; then
    fail "public WebDAV generated password is empty in secrets.json; start MnemoNAS once or set webdav.password before public access"
  elif password_risk="$(webdav_basic_password_risk "$password")"; then
    warn "public WebDAV generated Basic Auth password should be changed before public access (risk: $password_risk)"
  else
    ok "public WebDAV generated Basic Auth password is available"
  fi
}

check_public_config_file_path() {
  local symlink_component

  if [[ -L "$CONFIG_PATH" ]]; then
    fail "public config file path is a symlink; use a regular private config file: $CONFIG_PATH"
    return
  fi
  if symlink_component="$(first_symlink_path_component "$CONFIG_PATH")"; then
    fail "public config file path contains a symlink component; use a regular private config path: $symlink_component in $CONFIG_PATH"
    return
  fi
  ok "public config file path has no symlink components"
}

check_public_session_token_ttl() {
  local access_raw
  local refresh_raw
  local access_ttl
  local refresh_ttl
  local access_ns
  local refresh_ns
  local parse_status
  local recommended_access_ns
  local recommended_refresh_ns

  recommended_access_ns=$((60 * 60 * 1000000000))
  recommended_refresh_ns=$((30 * 24 * 60 * 60 * 1000000000))

  if [[ "$configured_auth_access_token_ttl_set" == "1" ]]; then
    access_raw="${configured_auth_access_token_ttl:-}"
  else
    access_raw="15m"
  fi
  if [[ "$configured_auth_refresh_token_ttl_set" == "1" ]]; then
    refresh_raw="${configured_auth_refresh_token_ttl:-}"
  else
    refresh_raw="168h"
  fi

  access_ttl="$(trim_ascii_whitespace "$access_raw")"
  refresh_ttl="$(trim_ascii_whitespace "$refresh_raw")"

  access_ns="$(go_duration_to_nanoseconds "$access_ttl")"
  parse_status=$?
  case "$parse_status" in
    0)
      ;;
    127)
      warn "python3 not available; skipping public auth token TTL duration check"
      return
      ;;
    *)
      fail "public auth.access_token_ttl must be a positive duration: ${access_ttl:-<empty>}"
      return
      ;;
  esac

  refresh_ns="$(go_duration_to_nanoseconds "$refresh_ttl")"
  parse_status=$?
  case "$parse_status" in
    0)
      ;;
    127)
      warn "python3 not available; skipping public auth token TTL duration check"
      return
      ;;
    *)
      fail "public auth.refresh_token_ttl must be a positive duration: ${refresh_ttl:-<empty>}"
      return
      ;;
  esac

  if (( access_ns <= 0 )); then
    fail "public auth.access_token_ttl must be a positive duration: ${access_ttl:-<empty>}"
  elif (( access_ns > recommended_access_ns )); then
    warn "public auth.access_token_ttl is longer than 1h: $access_ttl"
  else
    ok "public auth.access_token_ttl is within 1h: $access_ttl"
  fi

  if (( refresh_ns <= 0 )); then
    fail "public auth.refresh_token_ttl must be a positive duration: ${refresh_ttl:-<empty>}"
  elif (( refresh_ns > recommended_refresh_ns )); then
    warn "public auth.refresh_token_ttl is longer than 720h: $refresh_ttl"
  else
    ok "public auth.refresh_token_ttl is within 720h: $refresh_ttl"
  fi
}

clean_url_path_prefix() {
  local value="$1"
  local segment
  local last_index
  local -a input_segments=()
  local -a output_segments=()

  [[ "$value" == /* ]] || value="/$value"
  IFS='/' read -r -a input_segments <<< "$value"
  for segment in "${input_segments[@]}"; do
    case "$segment" in
      ""|.)
        ;;
      ..)
        if [[ "${#output_segments[@]}" -gt 0 ]]; then
          last_index=$(( ${#output_segments[@]} - 1 ))
          output_segments=("${output_segments[@]:0:$last_index}")
        fi
        ;;
      *)
        output_segments+=("$segment")
        ;;
    esac
  done

  if [[ "${#output_segments[@]}" -eq 0 ]]; then
    printf '/\n'
    return
  fi
  printf '/%s' "${output_segments[@]}"
  printf '\n'
}

url_path_escape() {
  local value="$1"
  local LC_ALL=C
  local escaped=""
  local char
  local encoded
  local i

  for ((i = 0; i < ${#value}; i++)); do
    char="${value:i:1}"
    case "$char" in
      [-._~/a-zA-Z0-9])
        escaped+="$char"
        ;;
      *)
        printf -v encoded '%%%02X' "'$char"
        escaped+="$encoded"
        ;;
    esac
  done

  printf '%s\n' "$escaped"
}

normalize_webdav_prefix_for_probe() {
  local trimmed
  local value

  trimmed="$(trim_ascii_whitespace "$1")"
  if [[ -z "$trimmed" ]]; then
    value="/dav"
  else
    value="$trimmed"
  fi
  if [[ "$value" != /* ]]; then
    value="/$value"
  fi

  [[ "$value" != *[[:cntrl:]]* ]] || return 1
  [[ "$value" != *\\* && "$value" != *\?* && "$value" != *\#* ]] || return 1

  value="$(clean_url_path_prefix "$value")"
  [[ "$value" != "/" ]] || return 1
  webdav_prefix_overlaps_reserved_route "$value" && return 1

  printf '%s\n' "$value"
}

normalize_configured_webdav_prefix_for_probe() {
  local value

  value="$(trim_ascii_whitespace "$1")"
  if [[ -z "$value" ]]; then
    value="/"
  elif [[ "$value" != /* ]]; then
    value="/$value"
  fi

  [[ "$value" != *[[:cntrl:]]* ]] || return 1
  [[ "$value" != *\\* && "$value" != *\?* && "$value" != *\#* ]] || return 1

  value="$(clean_url_path_prefix "$value")"
  [[ "$value" != "/" ]] || return 1
  webdav_prefix_overlaps_reserved_route "$value" && return 1

  printf '%s\n' "$value"
}

webdav_prefix_overlaps_reserved_route() {
  case "$1" in
    /api|/api/*|/s|/s/*|/health|/health/*)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

normalize_public_backend_host_for_loopback_check() {
  local value

  value="$(trim_ascii_whitespace "$1")"
  value="${value,,}"
  if [[ "$value" =~ ^\[([^][]+)\]$ ]]; then
    value="${BASH_REMATCH[1]}"
  fi
  printf '%s\n' "$value"
}

configured_webdav_prefix_display() {
  if [[ "$configured_webdav_prefix_set" != "1" ]]; then
    printf '<unset>\n'
  elif [[ -z "$configured_webdav_prefix" ]]; then
    printf '<empty>\n'
  else
    printf '%s\n' "$configured_webdav_prefix"
  fi
}

check_public_webdav_anonymous_rejected() {
  local domain="$1"
  local prefix="$2"
  local escaped_prefix
  local url
  local curl_result status http_code

  if ! have curl; then
    warn "curl not available; skipping public WebDAV anonymous access check"
    return
  fi

  escaped_prefix="$(url_path_escape "$prefix")"
  url="https://$domain$escaped_prefix/"

  curl_result="$(curl -sS --connect-timeout 3 --max-time 5 -o /dev/null -w '%{http_code}' -X PROPFIND -H 'Depth: 0' "$url" 2>/dev/null)"
  status=$?
  http_code="${curl_result//$'\n'/}"
  if [[ "$status" -ne 0 || ! "$http_code" =~ ^[0-9][0-9][0-9]$ || "$http_code" == "000" ]]; then
    warn "public WebDAV anonymous PROPFIND check was not readable: $url"
    return
  fi

  case "$http_code" in
    401|403)
      ok "public WebDAV anonymous PROPFIND is rejected: $url (HTTP $http_code)"
      ;;
    404|405)
      warn "public WebDAV endpoint did not answer PROPFIND at $url (HTTP $http_code); verify reverse-proxy WebDAV routing"
      ;;
    2??)
      fail "public WebDAV allows anonymous PROPFIND at $url (HTTP $http_code)"
      ;;
    *)
      warn "public WebDAV anonymous PROPFIND returned HTTP $http_code at $url; verify authentication before public use"
      ;;
  esac
}

count_enabled_admins() {
  local users_file="$1"

  python3 - "$users_file" <<'PY'
import json
import re
import sys

path = sys.argv[1]
try:
    with open(path, "r", encoding="utf-8") as handle:
        users = json.load(handle)
except Exception as exc:
    print(str(exc), file=sys.stderr)
    sys.exit(2)

if not isinstance(users, list):
    print("users file root is not a list", file=sys.stderr)
    sys.exit(2)

count = 0
seen_ids = set()
seen_usernames = set()
bcrypt_hash = re.compile(r"^\$2[aby]\$[0-9]{2}\$[./A-Za-z0-9]{53}$")
for index, user in enumerate(users):
    if not isinstance(user, dict):
        print(f"users file contains non-object entry at index {index}", file=sys.stderr)
        sys.exit(2)
    user_id = user.get("id")
    if not isinstance(user_id, str) or not user_id:
        print(f"users file contains user with empty id at index {index}", file=sys.stderr)
        sys.exit(2)
    if user_id in seen_ids:
        print(f"users file contains duplicate user id {user_id!r}", file=sys.stderr)
        sys.exit(2)
    seen_ids.add(user_id)
    username = user.get("username")
    if not isinstance(username, str) or not username.strip():
        print(f"users file contains invalid username at index {index}", file=sys.stderr)
        sys.exit(2)
    normalized_username = username.strip().lower()
    if normalized_username in seen_usernames:
        print(f"users file contains duplicate username {username!r}", file=sys.stderr)
        sys.exit(2)
    seen_usernames.add(normalized_username)
    role = user.get("role")
    if role not in {"admin", "user", "guest"}:
        print(f"users file contains invalid role for user {username!r}", file=sys.stderr)
        sys.exit(2)
    disabled = user.get("disabled", False)
    if not isinstance(disabled, bool):
        print(f"users file contains invalid disabled flag for user {username!r}", file=sys.stderr)
        sys.exit(2)
    if role == "admin" and not disabled:
        password_hash = user.get("password_hash")
        if not isinstance(password_hash, str) or not bcrypt_hash.match(password_hash):
            print(f"users file contains invalid password_hash for enabled administrator {username!r}", file=sys.stderr)
            sys.exit(2)
        count += 1
print(count)
PY
}

check_public_admin_redundancy() {
  local users_file
  local users_dir
  local active_admins
  local parse_error
  local symlink_component

  users_file="$(effective_auth_users_file)"
  users_dir="$(dirname "$users_file")"
  if is_false_value "${configured_auth_enabled:-true}"; then
    warn "public administrator redundancy check skipped because auth.enabled=false"
    return
  fi
  if [[ -L "$users_dir" ]]; then
    fail "public users file directory path is a symlink; use a regular private directory: $users_dir"
    return
  fi
  if symlink_component="$(first_symlink_path_component "$users_dir")"; then
    fail "public users file directory path contains a symlink component; use a regular private directory path: $symlink_component in $users_dir"
    return
  fi
  if [[ -L "$users_file" ]]; then
    fail "public users file path is a symlink; use a regular private file: $users_file"
    return
  fi
  if symlink_component="$(first_symlink_path_component "$users_file")"; then
    fail "public users file path contains a symlink component; use a regular private file path: $symlink_component in $users_file"
    return
  fi
  if [[ ! -f "$users_file" ]]; then
    fail "public users file is missing; cannot verify administrator redundancy: $users_file"
    return
  fi
  check_private_dir_mode "$users_dir" "public users file directory"
  check_private_file_mode "$users_file" "public users file"
  if ! have python3; then
    warn "python3 not available; skipping public administrator redundancy check"
    return
  fi

  parse_error="$(mktemp -t mnemonas-doctor-users.XXXXXX)"
  if active_admins="$(count_enabled_admins "$users_file" 2>"$parse_error")"; then
    rm -f -- "$parse_error"
  else
    fail "public users file could not be parsed; cannot verify administrator redundancy: $users_file"
    if [[ -s "$parse_error" ]]; then
      sed 's/^/[INFO] users.json parse error: /' "$parse_error"
    fi
    rm -f -- "$parse_error"
    return
  fi

  case "$active_admins" in
    ''|*[!0-9]*)
      fail "public users file returned an invalid administrator count: ${active_admins:-<empty>}"
      ;;
    0)
      fail "public users file has no enabled administrators"
      ;;
    1)
      warn "public administrator redundancy is weak: only one enabled administrator"
      ;;
    *)
      ok "public administrator redundancy verified: $active_admins enabled administrators"
      ;;
  esac
}

effective_auth_users_file() {
  if [[ -n "${configured_auth_users_file:-}" ]]; then
    printf '%s\n' "$configured_auth_users_file"
  else
    printf '%s\n' "$STORAGE_ROOT/.mnemonas/users.json"
  fi
}

initial_password_file() {
  local users_file

  users_file="$(effective_auth_users_file)"
  printf '%s\n' "$(dirname "$users_file")/initial-password.txt"
}

check_runtime_sensitive_files() {
  local users_file users_dir secrets_file

  users_file="$(effective_auth_users_file)"
  users_dir="$(dirname "$users_file")"
  secrets_file="$(effective_secrets_file)"

  if is_false_value "${configured_auth_enabled:-true}"; then
    note "auth.enabled=false; skipping users file availability check"
  else
    check_sensitive_dir_path "$users_dir" "users file directory"
    check_sensitive_file_path "$users_file" "users file" "users file is missing while auth.enabled=true"
  fi

  check_sensitive_file_path "$secrets_file" "generated secrets file"
}

check_admin_availability() {
  local users_file
  local active_admins
  local parse_error

  if is_false_value "${configured_auth_enabled:-true}"; then
    note "auth.enabled=false; skipping administrator availability check"
    return
  fi

  users_file="$(effective_auth_users_file)"
  if [[ -L "$users_file" || ! -f "$users_file" ]]; then
    return
  fi
  if ! have python3; then
    warn "python3 not available; skipping administrator availability check"
    return
  fi

  parse_error="$(mktemp -t mnemonas-doctor-users.XXXXXX)"
  if active_admins="$(count_enabled_admins "$users_file" 2>"$parse_error")"; then
    rm -f -- "$parse_error"
  else
    warn "users file could not be parsed; administrator availability cannot be verified: $users_file"
    if [[ -s "$parse_error" ]]; then
      sed 's/^/[INFO] users.json parse error: /' "$parse_error"
    fi
    rm -f -- "$parse_error"
    return
  fi

  case "$active_admins" in
    ''|*[!0-9]*)
      warn "users file returned an invalid administrator count: ${active_admins:-<empty>}"
      ;;
    0)
      warn "users file has no enabled administrators; MnemoNAS will create a recovery administrator on next startup if auth is enabled"
      ;;
    *)
      ok "administrator availability verified: $active_admins enabled administrator(s)"
      ;;
  esac
}

check_auth_posture() {
  local webdav_auth_type

  if is_false_value "${configured_auth_enabled:-true}"; then
    warn "auth.enabled=false; Web UI/API access relies on a controlled network, VPN, or outer access-control layer"
  else
    ok "Web UI/API authentication is enabled"
  fi

  webdav_auth_type="$(normalize_webdav_auth_type_for_public_check "$configured_webdav_auth_type")"
  if is_false_value "${configured_webdav_enabled:-true}"; then
    ok "WebDAV is disabled"
  elif [[ "$webdav_auth_type" == "none" ]]; then
    warn "WebDAV auth_type=none; restrict access with loopback binding, VPN, firewall, or another trusted boundary"
  else
    ok "WebDAV authentication is configured: $webdav_auth_type"
  fi

  if is_true_value "${configured_allow_unsafe_no_auth:-false}"; then
    warn "security.allow_unsafe_no_auth=true; verify that an outer boundary deliberately restricts unauthenticated access"
  fi
}

report_initial_password_issue() {
  local severity="$1"
  local message="$2"

  if [[ "$severity" == "fail" ]]; then
    fail "$message"
  else
    warn "$message"
  fi
}

check_initial_password_file_absent() {
  local path="$1"
  local severity="$2"
  local remediation="$3"
  local symlink_component

  if [[ -L "$path" ]]; then
    report_initial_password_issue "$severity" "initial admin password path is a symlink at $path; remove it before public or shared use"
  elif symlink_component="$(first_symlink_path_component "$path")"; then
    report_initial_password_issue "$severity" "initial admin password path contains a symlink component at $symlink_component in $path; use a regular private auth directory before public or shared use"
  elif [[ -f "$path" ]]; then
    report_initial_password_issue "$severity" "initial admin password file still exists at $path; $remediation"
  elif [[ -e "$path" ]]; then
    report_initial_password_issue "$severity" "initial admin password path exists but is not a regular file at $path; remove it before public or shared use"
  else
    ok "initial admin password file is absent"
  fi
}

format_kib() {
  local value="$1"
  awk -v kib="$value" '
    BEGIN {
      split("KiB MiB GiB TiB PiB", units, " ")
      size = kib
      unit = 1
      while (size >= 1024 && unit < 5) {
        size = size / 1024
        unit++
      }
      printf "%.1f %s", size, units[unit]
    }
  '
}

check_disk_space() {
  local path="$1"
  [[ -d "$path" ]] || return
  if ! have df; then
    warn "df not available; skipping storage free-space check"
    return
  fi
  if [[ ! "$MIN_FREE_BYTES" =~ ^[0-9]+$ ]]; then
    warn "MIN_FREE_BYTES is not numeric; skipping storage free-space threshold check"
    return
  fi

  local total_kib available_kib used_percent min_free_kib
  read -r total_kib available_kib used_percent < <(df -Pk "$path" 2>/dev/null | awk 'NR == 2 { print $2, $4, $5 }')
  if [[ -z "${total_kib:-}" || -z "${available_kib:-}" ]]; then
    warn "could not read storage free space for $path"
    return
  fi

  ok "storage disk space: $(format_kib "$available_kib") available / $(format_kib "$total_kib") total ($used_percent used)"
  min_free_kib=$((MIN_FREE_BYTES / 1024))
  if (( available_kib < min_free_kib )); then
    warn "storage free space is below $(format_kib "$min_free_kib"); clean old data or expand the disk"
  fi
}

real_path() {
  local path="$1"

  have python3 || return 127
  python3 - "$path" <<'PY'
import os
import sys

print(os.path.realpath(sys.argv[1]))
PY
}

path_contains_or_equals() {
  local parent="$1"
  local child="$2"

  [[ "$child" == "$parent" || "$child" == "$parent"/* ]]
}

check_backup_root() {
  local storage_real backup_real storage_source backup_source

  if [[ -z "$BACKUP_ROOT" ]]; then
    warn "backup root is empty; configure an independent backup target"
    return
  fi
  if [[ "$BACKUP_ROOT" != /* ]]; then
    warn "backup root is not absolute: $BACKUP_ROOT"
    return
  fi

  if have python3; then
    storage_real="$(real_path "$STORAGE_ROOT")"
    backup_real="$(real_path "$BACKUP_ROOT")"
    if path_contains_or_equals "$storage_real" "$backup_real"; then
      fail "backup root must not be inside storage root: $BACKUP_ROOT (storage: $STORAGE_ROOT). Use a separate disk, dataset, or remote target."
    else
      ok "backup root is outside storage root: $BACKUP_ROOT"
    fi
  else
    warn "python3 not available; skipping backup root containment check"
  fi

  if [[ -L "$BACKUP_ROOT" ]]; then
    warn "backup root path is a symlink; use a real directory, dataset, mount point, or remote target: $BACKUP_ROOT"
  fi
  if [[ ! -e "$BACKUP_ROOT" ]]; then
    warn "backup root not found: $BACKUP_ROOT"
    return
  fi
  if [[ ! -d "$BACKUP_ROOT" ]]; then
    warn "backup root is not a directory: $BACKUP_ROOT"
    return
  fi

  ok "backup root exists: $BACKUP_ROOT"
  check_service_user_writable_dir "$BACKUP_ROOT" "backup root"
  if have findmnt; then
    storage_source="$(findmnt -no SOURCE "$STORAGE_ROOT" 2>/dev/null | awk 'NR == 1 { print $1 }')"
    backup_source="$(findmnt -no SOURCE "$BACKUP_ROOT" 2>/dev/null | awk 'NR == 1 { print $1 }')"
    if [[ -z "$storage_source" || -z "$backup_source" ]]; then
      warn "could not compare storage and backup filesystem sources"
    elif [[ "$storage_source" == "$backup_source" ]]; then
      warn "backup root shares filesystem source with storage root ($storage_source); use a separate disk, dataset, or remote target for failure isolation"
    else
      ok "backup root is on a separate filesystem source: $backup_source"
    fi
  fi
}

check_systemd_unit() {
  local unit="$1"
  if ! have systemctl; then
    warn "systemctl not available; skipping $unit"
    return
  fi
  if systemctl is-active --quiet "$unit"; then
    ok "$unit is active"
  else
    fail "$unit is not active"
    systemctl status "$unit" --no-pager 2>/dev/null | tail -n 20 || true
  fi
}

ufw_allows_port() {
  local status="$1"
  local port="$2"
  awk -v port="$port" '
    BEGIN { found = 0 }
    tolower($0) ~ /allow/ && $0 ~ "(^|[^0-9])" port "(/tcp)?([^0-9]|$)" {
      found = 1
    }
    END { exit found ? 0 : 1 }
  ' <<< "$status"
}

ufw_broadly_allows_port() {
  local status="$1"
  local port="$2"
  awk -v port="$port" '
    BEGIN { found = 0 }
    tolower($0) ~ /allow/ &&
      $0 ~ "(^|[^0-9])" port "(/tcp)?([^0-9]|$)" &&
      tolower($0) ~ /(anywhere|0\.0\.0\.0\/0|::\/0)/ {
      found = 1
    }
    END { exit found ? 0 : 1 }
  ' <<< "$status"
}

check_ufw_backend_port_policy() {
  local status="$1"
  local port="$2"
  local label="$3"

  if ! ufw_allows_port "$status" "$port"; then
    return
  fi

  if [[ -n "$PUBLIC_DOMAIN" ]] && ufw_broadly_allows_port "$status" "$port"; then
    fail "ufw appears to broadly allow public $label port $port; remove allow rules for this backend port before public access"
  else
    warn "ufw appears to allow $label port $port; remove public allow rules for this port"
  fi
}

check_ufw() {
  if ! have ufw; then
    return
  fi

  local status
  status="$(ufw status 2>/dev/null || true)"
  if grep -qi 'Status:[[:space:]]*active' <<< "$status"; then
    ok "ufw is active"
  elif grep -qi 'Status:[[:space:]]*inactive' <<< "$status"; then
    warn "ufw is installed but inactive; restrict access to port $SERVER_PORT and keep dataplane ports private"
  else
    warn "could not read ufw status"
  fi

  if [[ -n "$PUBLIC_DOMAIN" ]]; then
    check_ufw_backend_port_policy "$status" "$SERVER_PORT" "control plane"
  fi
  check_ufw_backend_port_policy "$status" "$DATAPLANE_GRPC_PORT" "dataplane gRPC"
  check_ufw_backend_port_policy "$status" "$DATAPLANE_HTTP_PORT" "dataplane HTTP"
}

check_public_domain() {
  local domain="$1"
  local domain_lower="${domain,,}"
  local public_health_url="https://$domain/health"
  local public_webdav_auth_type
  local public_webdav_password_risk
  local public_webdav_prefix
  local public_share_base_url
  local public_share_base_url_log
  local public_share_base_url_raw
  local public_share_base_url_lower
  local public_share_host
  local public_share_host_normalized
  local public_share_port
  local public_backend_host

  [[ -n "$domain" ]] || return

  printf '\nPublic access checks for %s\n' "$domain"

  check_public_config_file_path
  check_public_curl_available
  check_public_python_available
  check_public_getent_available
  check_public_dns_resolution "$domain"

  public_backend_host="$(normalize_public_backend_host_for_loopback_check "$configured_server_host")"
  if is_loopback_host "$public_backend_host"; then
    ok "public backend host is loopback-only: ${configured_server_host:-<unset>}"
  else
    fail "public backend host should be 127.0.0.1 behind a reverse proxy, got: ${configured_server_host:-<unset>}"
  fi

  if [[ "$configured_trusted_proxy_hops" =~ ^[0-9]+$ ]] && (( 10#$configured_trusted_proxy_hops >= 1 )); then
    ok "trusted proxy hops configured: $configured_trusted_proxy_hops"
  else
    fail "server.trusted_proxy_hops should be at least 1 behind a public reverse proxy, got: ${configured_trusted_proxy_hops:-<unset>}"
  fi

  if is_false_value "${configured_auth_enabled:-true}"; then
    fail "public auth.enabled must remain true"
  else
    ok "public auth.enabled is enabled"
    check_public_session_token_ttl
  fi
  check_public_admin_redundancy

  if is_true_value "${configured_allow_unsafe_no_auth:-false}"; then
    fail "security.allow_unsafe_no_auth must be false for public deployments"
  else
    ok "security.allow_unsafe_no_auth is not enabled"
  fi

  public_webdav_auth_type="$(normalize_webdav_auth_type_for_public_check "$configured_webdav_auth_type")"
  if is_false_value "${configured_webdav_enabled:-true}"; then
    ok "public WebDAV is disabled"
  elif [[ "$public_webdav_auth_type" == "none" ]]; then
    fail "public WebDAV must not use auth_type=none"
  else
    if [[ "$public_webdav_auth_type" == "basic" ]]; then
      if [[ -z "$(trim_ascii_whitespace "$configured_webdav_password")" ]]; then
        check_public_webdav_generated_password
      elif public_webdav_password_risk="$(webdav_basic_password_risk "$configured_webdav_password")"; then
        warn "public WebDAV Basic Auth password should be changed before public access (risk: $public_webdav_password_risk)"
      else
        ok "public WebDAV is configured with Basic Auth"
      fi
    else
      ok "public WebDAV is configured with authentication"
    fi
    if [[ "$configured_webdav_prefix_set" == "1" ]]; then
      public_webdav_prefix="$(normalize_configured_webdav_prefix_for_probe "$configured_webdav_prefix")" || public_webdav_prefix=""
    else
      public_webdav_prefix="$(normalize_webdav_prefix_for_probe "")" || public_webdav_prefix=""
    fi
    if [[ -n "$public_webdav_prefix" ]]; then
      check_public_webdav_anonymous_rejected "$domain" "$public_webdav_prefix"
    else
      fail "public WebDAV prefix is invalid: $(configured_webdav_prefix_display)"
    fi
  fi

  if is_true_value "${configured_share_enabled:-false}"; then
    public_share_base_url_raw="${configured_share_base_url:-}"
    public_share_base_url="$(trim_ascii_whitespace "$public_share_base_url_raw")"
    public_share_base_url_log="$(redact_http_url_for_log "$public_share_base_url")"
    public_share_base_url_lower="${public_share_base_url,,}"
    if [[ -z "$public_share_base_url" ]]; then
      warn "public share.base_url is empty; generated share links may be relative instead of https://$domain"
    elif [[ "$public_share_base_url_lower" != https://* ]]; then
      fail "public share.base_url must use https: $public_share_base_url_log"
    elif http_url_has_userinfo "$public_share_base_url"; then
      fail "public share.base_url must not include userinfo: $public_share_base_url_log"
    elif http_url_has_query_or_fragment "$public_share_base_url"; then
      fail "public share.base_url must not include query or fragment: $public_share_base_url_log"
    elif http_url_authority_has_invalid_host_port_syntax "$public_share_base_url"; then
      fail "public share.base_url host is invalid: $public_share_base_url_log"
    else
      public_share_port="$(http_url_port "$public_share_base_url")"
      if [[ -n "$public_share_port" && "$public_share_port" != "443" ]]; then
        fail "public share.base_url must use the HTTPS default port 443: $public_share_base_url_log"
      else
        public_share_host="$(http_url_host "$public_share_base_url")"
        if ! http_url_host_is_valid "$public_share_host"; then
          fail "public share.base_url host is invalid: $public_share_base_url_log"
        else
          public_share_host_normalized="${public_share_host%.}"
          public_share_host_normalized="${public_share_host_normalized,,}"
          if [[ "$public_share_host_normalized" == "$domain_lower" ]]; then
            ok "public share.base_url uses HTTPS on $domain"
          else
            warn "public share.base_url host does not match $domain: $public_share_base_url_log"
          fi
          local public_share_path
          public_share_path="$(http_url_path "$public_share_base_url")"
          if http_url_path_has_backslashes "$public_share_path"; then
            fail "public share.base_url path must not contain backslashes: $public_share_base_url_log"
          elif http_url_path_has_query_or_fragment_markers "$public_share_path"; then
            fail "public share.base_url must not include query or fragment: $public_share_base_url_log"
          elif http_url_path_has_duplicate_slashes "$public_share_path"; then
            fail "public share.base_url path must not contain duplicate slashes: $public_share_base_url_log"
          elif http_url_path_has_dot_segments "$public_share_path"; then
            fail "public share.base_url path must not contain . or .. segments: $public_share_base_url_log"
          elif http_url_path_ends_with_share_route "$public_share_path"; then
            warn "public share.base_url should be the site origin or base path before /s; current value will generate nested /s/s share links: $public_share_base_url_log"
          fi
        fi
      fi
    fi
    check_public_share_default_policy
    check_public_share_response_boundary "$domain"
  else
    ok "public share links are disabled"
  fi

  if have curl && curl -fsS "$public_health_url" >/dev/null 2>&1; then
    ok "public HTTPS health reachable: $public_health_url"
  else
    fail "public HTTPS health not reachable: $public_health_url"
  fi
  check_http_redirects_to_https "$domain"
  PUBLIC_CERT_FAILURE=0
  check_https_certificate "$domain"
  check_certificate_renewal_automation
  if [[ "$PUBLIC_CERT_FAILURE" -eq 1 ]]; then
    print_certificate_failure_guidance "$domain"
  fi

  check_http_unreachable "http://$domain:$SERVER_PORT/health" "public direct control plane"
  check_tcp_unreachable "$domain" "$SERVER_PORT" "public direct control plane TCP"
  check_tcp_unreachable "$domain" "$DATAPLANE_GRPC_PORT" "public dataplane gRPC"
  check_tcp_unreachable "$domain" "$DATAPLANE_HTTP_PORT" "public dataplane HTTP"

  check_loopback_only_port_strict "$SERVER_PORT" "control plane"
  check_loopback_only_port_strict "$DATAPLANE_GRPC_PORT" "dataplane gRPC"
  check_loopback_only_port_strict "$DATAPLANE_HTTP_PORT" "dataplane HTTP"

  local password_file
  password_file="$(initial_password_file)"
  check_initial_password_file_absent "$password_file" fail "change the admin password before public access"

  note "manual cloud firewall check: expose only 80/443 publicly; keep $SERVER_PORT/$DATAPLANE_GRPC_PORT/$DATAPLANE_HTTP_PORT closed to the public internet"
}

printf 'MnemoNAS deployment doctor\n'
printf 'Config: %s\n' "$CONFIG_PATH"
printf 'Storage: %s\n' "$STORAGE_ROOT"
printf '\n'

check_executable_file "$BIN_DIR/nasd" "nasd binary"
check_executable_file "$BIN_DIR/dataplane" "dataplane binary"
check_config_file "$CONFIG_PATH"
check_config_toml_syntax "$CONFIG_PATH"
check_file "$WEB_DIR/index.html" "Web UI index"
check_dir "$STORAGE_ROOT" "storage root"
check_dir "$STORAGE_ROOT/.mnemonas" "internal metadata root"
check_service_user_writable_dir "$STORAGE_ROOT" "storage root"
check_service_user_writable_dir "$STORAGE_ROOT/files" "files directory"
check_service_user_writable_dir "$STORAGE_ROOT/.mnemonas" "internal metadata root"
check_no_other_access "$STORAGE_ROOT" "storage root"
check_no_other_access "$STORAGE_ROOT/files" "files directory"
check_private_dir_mode "$STORAGE_ROOT/.mnemonas" "internal metadata root"

if [[ -x "$BIN_DIR/nasd" && -f "$CONFIG_PATH" ]]; then
  if [[ "$CONFIG_TOML_SYNTAX_VALID" == "0" ]]; then
    note "skipping nasd --check-config because config TOML syntax is invalid"
  else
    config_check_out="$(mktemp -t mnemonas-doctor-check-config.XXXXXX)"
    if "$BIN_DIR/nasd" --check-config --config "$CONFIG_PATH" >"$config_check_out" 2>&1; then
      ok "config validates"
    else
      fail "config validation failed"
      cat "$config_check_out"
    fi
    rm -f -- "$config_check_out"
  fi
fi

if id -u "$SERVICE_USER" >/dev/null 2>&1; then
  ok "service user exists: $SERVICE_USER"
else
  fail "service user missing: $SERVICE_USER"
fi

check_systemd_unit mnemonas-dataplane.service
check_systemd_unit mnemonas.service

check_http "$DATAPLANE_URL/health" "dataplane health"
check_http "$SERVER_URL/health" "control plane health"

if have curl && curl -fsS -H 'Accept: text/html' "$SERVER_URL/" | grep -q 'id="root"'; then
  ok "Web UI root route returns HTML"
else
  fail "Web UI root route did not return the app shell"
fi

if can_inspect_local_ports; then
  port_source="$(port_inspection_source)"
  if port_listening "$SERVER_PORT"; then
    ok "control plane port $SERVER_PORT is listening"
  else
    warn "control plane port $SERVER_PORT is not visible in $port_source"
  fi
  if port_listening "$DATAPLANE_GRPC_PORT"; then
    ok "dataplane gRPC port $DATAPLANE_GRPC_PORT is listening"
    check_loopback_only_port "$DATAPLANE_GRPC_PORT" "dataplane gRPC"
  else
    warn "dataplane gRPC port $DATAPLANE_GRPC_PORT is not visible in $port_source"
  fi
  if port_listening "$DATAPLANE_HTTP_PORT"; then
    ok "dataplane HTTP port $DATAPLANE_HTTP_PORT is listening"
    check_loopback_only_port "$DATAPLANE_HTTP_PORT" "dataplane HTTP"
  else
    warn "dataplane HTTP port $DATAPLANE_HTTP_PORT is not visible in $port_source"
  fi
else
  warn "local port table cannot be inspected; install iproute2/ss or make /proc/net/tcp readable"
fi

if have findmnt; then
  mount_info="$(findmnt -no SOURCE,FSTYPE,TARGET "$STORAGE_ROOT" 2>/dev/null || true)"
  if [[ -n "$mount_info" ]]; then
    ok "storage mount: $mount_info"
    case "$mount_info" in
      *zfs*|*btrfs*)
        ok "storage filesystem has native checksum/scrub support"
        ;;
      *)
        warn "storage filesystem is not ZFS/Btrfs; rely on MnemoNAS scrub plus backups for integrity"
        ;;
    esac
  else
    warn "could not resolve storage mount for $STORAGE_ROOT"
  fi
fi

check_disk_space "$STORAGE_ROOT"

check_auth_posture
check_runtime_sensitive_files
check_admin_availability

password_file="$(initial_password_file)"
check_initial_password_file_absent "$password_file" warn "log in once and change the password"

check_backup_root

if have zpool; then
  zpool_out="$(mktemp -t mnemonas-doctor-zpool.XXXXXX)"
  if zpool status >"$zpool_out" 2>&1; then
    ok "zpool status is available"
  else
    warn "zpool status reported issues"
    tail -n 30 "$zpool_out"
  fi
  rm -f -- "$zpool_out"
fi

if have tailscale; then
  if tailscale status >/dev/null 2>&1; then
    ok "tailscale status is available"
  else
    warn "tailscale is installed but not healthy"
  fi
fi

check_ufw
check_public_domain "$PUBLIC_DOMAIN"

printf '\nSummary: %d failure(s), %d warning(s)\n' "$FAILURES" "$WARNINGS"
if [[ "$FAILURES" -gt 0 ]]; then
  exit 1
fi
exit 0
