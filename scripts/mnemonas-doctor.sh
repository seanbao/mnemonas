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

FAILURES=0
WARNINGS=0

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
      found = 1
      exit
    }
    END {
      exit found ? 0 : 1
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

port_listening() {
  local port="$1"
  ss_local_addresses_for_port "$port" | grep -q .
}

ss_local_addresses_for_port() {
  local port="$1"
  ss -lntH | awk -v suffix=":$port" '$4 ~ suffix "$" { print $4 }'
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

  while IFS= read -r address; do
    [[ -n "$address" ]] || continue
    host="$(host_from_ss_local_address "$address" "$port")"
    if ! is_loopback_host "$host"; then
      unsafe_addresses+=("$address")
    fi
  done < <(ss_local_addresses_for_port "$port")

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

  while IFS= read -r address; do
    [[ -n "$address" ]] || continue
    host="$(host_from_ss_local_address "$address" "$port")"
    if ! is_loopback_host "$host"; then
      unsafe_addresses+=("$address")
    fi
  done < <(ss_local_addresses_for_port "$port")

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

  [[ -z "$value" ]] && return 0
  [[ "$value" != *[[:space:]]* ]] || die "PUBLIC_DOMAIN cannot contain whitespace: $value"
  [[ "$value" != *[[:cntrl:]]* ]] || die "PUBLIC_DOMAIN cannot contain control characters: $value"
  [[ "$value" != *"/"* && "$value" != *":"* ]] || die "PUBLIC_DOMAIN must be a hostname without scheme or port: $value"
  is_valid_tcp_host "$value" || die "PUBLIC_DOMAIN is invalid: $value"
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
configured_webdav_enabled=""
configured_webdav_auth_type=""
configured_webdav_prefix=""
configured_webdav_prefix_set=0
configured_webdav_password=""
configured_allow_unsafe_no_auth=""
configured_share_enabled=""
configured_share_base_url=""
if [[ -f "$CONFIG_PATH" ]]; then
  configured_server_host="$(toml_value server host "$CONFIG_PATH")"
  configured_storage_root="$(toml_value storage root "$CONFIG_PATH")"
  configured_server_port="$(toml_value server port "$CONFIG_PATH")"
  configured_trusted_proxy_hops="$(toml_value server trusted_proxy_hops "$CONFIG_PATH")"
  configured_grpc_address="$(toml_value dataplane grpc_address "$CONFIG_PATH")"
  configured_auth_enabled="$(toml_value auth enabled "$CONFIG_PATH")"
  configured_auth_users_file="$(toml_value auth users_file "$CONFIG_PATH")"
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
fi
configured_http_address="$(systemd_env_value DATAPLANE_HTTP_ADDR "$SYSTEMD_DIR/mnemonas-dataplane.service")"

STORAGE_ROOT="${STORAGE_ROOT:-${configured_storage_root:-/srv/mnemonas}}"
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
    warn "curl not available; skipping public HTTP-to-HTTPS redirect check"
    return
  fi

  curl_result="$(curl -sS -I --connect-timeout 3 --max-time 5 -o /dev/null -w '%{http_code} %{redirect_url}' "$url" 2>/dev/null)"
  status=$?
  if [[ "$status" -ne 0 ]]; then
    warn "public HTTP redirect check was not reachable: $url"
    return
  fi

  http_code="${curl_result%% *}"
  redirect_url="${curl_result#* }"
  if [[ "$http_code" =~ ^30(1|2|3|7|8)$ ]] && https_redirect_targets_domain "$domain" "$redirect_url"; then
    ok "public HTTP redirects to HTTPS: $url -> $redirect_url"
  else
    warn "public HTTP does not clearly redirect to HTTPS: $url returned ${http_code:-unknown}"
  fi
}

check_https_certificate() {
  local domain="$1"
  local cert_out cert_err status enddate

  if ! have openssl; then
    warn "openssl not available; skipping public HTTPS certificate check"
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

http_url_path() {
  local value="$1"
  local without_scheme remainder path_part

  without_scheme="${value#*://}"
  if [[ "$without_scheme" != */* ]]; then
    printf '/\n'
    return
  fi
  remainder="/${without_scheme#*/}"
  path_part="${remainder%%\?*}"
  path_part="${path_part%%#*}"
  [[ -n "$path_part" ]] || path_part="/"
  printf '%s\n' "$path_part"
}

http_url_path_ends_with_share_route() {
  local path="$1"

  while [[ "$path" != "/" && "$path" == */ ]]; do
    path="${path%/}"
  done
  [[ "$path" == "/s" || "$path" == */s ]]
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
  local url="https://$domain$prefix/"
  local curl_result status http_code

  if ! have curl; then
    warn "curl not available; skipping public WebDAV anonymous access check"
    return
  fi

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
for user in users:
    if not isinstance(user, dict):
        continue
    if user.get("role") == "admin" and not bool(user.get("disabled", False)):
        count += 1
print(count)
PY
}

check_public_admin_redundancy() {
  local users_file
  local active_admins
  local parse_error

  users_file="$(effective_auth_users_file)"
  if is_false_value "${configured_auth_enabled:-true}"; then
    warn "public administrator redundancy check skipped because auth.enabled=false"
    return
  fi
  if [[ ! -f "$users_file" ]]; then
    fail "public users file is missing; cannot verify administrator redundancy: $users_file"
    return
  fi
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

  if ufw_allows_port "$status" "$DATAPLANE_GRPC_PORT"; then
    warn "ufw appears to allow dataplane gRPC port $DATAPLANE_GRPC_PORT; remove public allow rules for this port"
  fi
  if ufw_allows_port "$status" "$DATAPLANE_HTTP_PORT"; then
    warn "ufw appears to allow dataplane HTTP port $DATAPLANE_HTTP_PORT; remove public allow rules for this port"
  fi
}

check_public_domain() {
  local domain="$1"
  local domain_lower="${domain,,}"
  local public_health_url="https://$domain/health"
  local public_webdav_auth_type
  local public_webdav_password_risk
  local public_webdav_prefix
  local public_share_base_url
  local public_share_base_url_raw
  local public_share_base_url_lower
  local public_share_host
  local public_share_host_normalized
  local public_share_port
  local public_backend_host

  [[ -n "$domain" ]] || return

  printf '\nPublic access checks for %s\n' "$domain"

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
      if public_webdav_password_risk="$(webdav_basic_password_risk "$configured_webdav_password")"; then
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
    public_share_base_url_lower="${public_share_base_url,,}"
    if [[ -z "$public_share_base_url" ]]; then
      warn "public share.base_url is empty; generated share links may be relative instead of https://$domain"
    elif [[ "$public_share_base_url_lower" != https://* ]]; then
      fail "public share.base_url must use https: $public_share_base_url"
    elif http_url_has_userinfo "$public_share_base_url"; then
      fail "public share.base_url must not include userinfo: $public_share_base_url"
    elif http_url_has_query_or_fragment "$public_share_base_url"; then
      fail "public share.base_url must not include query or fragment: $public_share_base_url"
    else
      public_share_port="$(http_url_port "$public_share_base_url")"
      if [[ -n "$public_share_port" && "$public_share_port" != "443" ]]; then
        fail "public share.base_url must use the HTTPS default port 443: $public_share_base_url"
      else
        public_share_host="$(http_url_host "$public_share_base_url")"
        if ! http_url_host_is_valid "$public_share_host"; then
          fail "public share.base_url host is invalid: $public_share_base_url"
        else
          public_share_host_normalized="${public_share_host%.}"
          public_share_host_normalized="${public_share_host_normalized,,}"
          if [[ "$public_share_host_normalized" == "$domain_lower" ]]; then
            ok "public share.base_url uses HTTPS on $domain"
          else
            warn "public share.base_url host does not match $domain: $public_share_base_url"
          fi
          if http_url_path_ends_with_share_route "$(http_url_path "$public_share_base_url")"; then
            warn "public share.base_url should be the site origin or base path before /s; current value will generate nested /s/s share links: $public_share_base_url"
          fi
        fi
      fi
    fi
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
  check_tcp_unreachable "$domain" "$DATAPLANE_GRPC_PORT" "public dataplane gRPC"
  check_tcp_unreachable "$domain" "$DATAPLANE_HTTP_PORT" "public dataplane HTTP"

  if have ss; then
    check_loopback_only_port_strict "$SERVER_PORT" "control plane"
    check_loopback_only_port_strict "$DATAPLANE_GRPC_PORT" "dataplane gRPC"
    check_loopback_only_port_strict "$DATAPLANE_HTTP_PORT" "dataplane HTTP"
  fi

  local password_file
  password_file="$(initial_password_file)"
  if [[ -f "$password_file" ]]; then
    fail "initial admin password file still exists at $password_file; change the admin password before public access"
  fi

  note "manual cloud firewall check: expose only 80/443 publicly; keep $SERVER_PORT/$DATAPLANE_GRPC_PORT/$DATAPLANE_HTTP_PORT closed to the public internet"
}

printf 'MnemoNAS deployment doctor\n'
printf 'Config: %s\n' "$CONFIG_PATH"
printf 'Storage: %s\n' "$STORAGE_ROOT"
printf '\n'

check_executable_file "$BIN_DIR/nasd" "nasd binary"
check_executable_file "$BIN_DIR/dataplane" "dataplane binary"
check_file "$CONFIG_PATH" "config"
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
  config_check_out="$(mktemp -t mnemonas-doctor-check-config.XXXXXX)"
  if "$BIN_DIR/nasd" --check-config --config "$CONFIG_PATH" >"$config_check_out" 2>&1; then
    ok "config validates"
  else
    fail "config validation failed"
    cat "$config_check_out"
  fi
  rm -f -- "$config_check_out"
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

if have ss; then
  if port_listening "$SERVER_PORT"; then
    ok "control plane port $SERVER_PORT is listening"
  else
    warn "control plane port $SERVER_PORT is not visible in ss output"
  fi
  if port_listening "$DATAPLANE_GRPC_PORT"; then
    ok "dataplane gRPC port $DATAPLANE_GRPC_PORT is listening"
    check_loopback_only_port "$DATAPLANE_GRPC_PORT" "dataplane gRPC"
  else
    warn "dataplane gRPC port $DATAPLANE_GRPC_PORT is not visible in ss output"
  fi
  if port_listening "$DATAPLANE_HTTP_PORT"; then
    ok "dataplane HTTP port $DATAPLANE_HTTP_PORT is listening"
    check_loopback_only_port "$DATAPLANE_HTTP_PORT" "dataplane HTTP"
  else
    warn "dataplane HTTP port $DATAPLANE_HTTP_PORT is not visible in ss output"
  fi
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

password_file="$(initial_password_file)"
if [[ -f "$password_file" ]]; then
  warn "initial admin password file still exists at $password_file; log in once and change the password"
else
  ok "initial admin password file is absent"
fi

if [[ -d "$BACKUP_ROOT" ]]; then
  ok "backup root exists: $BACKUP_ROOT"
else
  warn "backup root not found: $BACKUP_ROOT"
fi

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
