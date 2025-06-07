#!/usr/bin/env bash

set -u

SERVICE_USER="${SERVICE_USER:-mnemonas}"
BIN_DIR="${BIN_DIR:-/usr/local/bin}"
WEB_DIR="${WEB_DIR:-/usr/local/share/mnemonas/web}"
CONFIG_PATH="${CONFIG_PATH:-/etc/mnemonas/config.toml}"
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
  [[ "$value" =~ ^[0-9]+$ ]] || die "$label must be numeric: $value"
  (( 10#$value >= 1 && 10#$value <= 65535 )) || die "$label must be between 1 and 65535: $value"
}

require_safe_tcp_addr() {
  local value="$1"
  local label="$2"
  local host=""
  local port=""

  [[ -n "$value" ]] || die "$label cannot be empty"
  [[ "$value" != *[[:space:]]* ]] || die "$label cannot contain whitespace: $value"

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
  [[ "$value" =~ ^https?://[^[:space:]]+$ ]] || die "$label must be an http(s) URL: $value"
}

require_safe_public_domain() {
  local value="$1"

  [[ -z "$value" ]] && return 0
  [[ "$value" != *[[:space:]]* ]] || die "PUBLIC_DOMAIN cannot contain whitespace: $value"
  [[ "$value" != *"/"* && "$value" != *":"* ]] || die "PUBLIC_DOMAIN must be a hostname without scheme or port: $value"
  is_valid_tcp_host "$value" || die "PUBLIC_DOMAIN is invalid: $value"
}

parse_args "$@"
require_safe_public_domain "$PUBLIC_DOMAIN"

configured_storage_root=""
configured_server_port=""
configured_grpc_address=""
configured_server_host=""
configured_trusted_proxy_hops=""
if [[ -f "$CONFIG_PATH" ]]; then
  configured_server_host="$(toml_value server host "$CONFIG_PATH")"
  configured_storage_root="$(toml_value storage root "$CONFIG_PATH")"
  configured_server_port="$(toml_value server port "$CONFIG_PATH")"
  configured_trusted_proxy_hops="$(toml_value server trusted_proxy_hops "$CONFIG_PATH")"
  configured_grpc_address="$(toml_value dataplane grpc_address "$CONFIG_PATH")"
fi

STORAGE_ROOT="${STORAGE_ROOT:-${configured_storage_root:-/srv/mnemonas}}"
SERVER_PORT="${SERVER_PORT:-${configured_server_port:-8080}}"
DATAPLANE_GRPC_ADDR="${DATAPLANE_GRPC_ADDR:-${configured_grpc_address:-127.0.0.1:9090}}"
DATAPLANE_GRPC_PORT="${DATAPLANE_GRPC_PORT:-${DATAPLANE_GRPC_ADDR##*:}}"
DATAPLANE_HTTP_PORT="${DATAPLANE_HTTP_PORT:-9091}"
SERVER_URL="${SERVER_URL:-http://127.0.0.1:$SERVER_PORT}"
DATAPLANE_URL="${DATAPLANE_URL:-http://127.0.0.1:$DATAPLANE_HTTP_PORT}"

require_safe_tcp_port "$SERVER_PORT" "SERVER_PORT"
require_safe_tcp_addr "$DATAPLANE_GRPC_ADDR" "DATAPLANE_GRPC_ADDR"
require_safe_tcp_port "$DATAPLANE_GRPC_PORT" "DATAPLANE_GRPC_PORT"
require_safe_tcp_port "$DATAPLANE_HTTP_PORT" "DATAPLANE_HTTP_PORT"
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
  if ! have curl; then
    warn "curl not available; skipping $label exposure check"
    return
  fi

  if curl -fsS --connect-timeout 3 --max-time 5 "$url" >/dev/null 2>&1; then
    fail "$label is publicly reachable: $url"
  else
    ok "$label is not publicly reachable: $url"
  fi
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
  if [[ "$http_code" =~ ^30(1|2|3|7|8)$ && "$redirect_url" == https://"$domain"* ]]; then
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
  local public_health_url="https://$domain/health"

  [[ -n "$domain" ]] || return

  printf '\nPublic access checks for %s\n' "$domain"

  if [[ "$configured_server_host" == "127.0.0.1" || "$configured_server_host" == "localhost" || "$configured_server_host" == "::1" || "$configured_server_host" == "[::1]" ]]; then
    ok "public backend host is loopback-only: ${configured_server_host:-<unset>}"
  else
    fail "public backend host should be 127.0.0.1 behind a reverse proxy, got: ${configured_server_host:-<unset>}"
  fi

  if [[ "$configured_trusted_proxy_hops" =~ ^[0-9]+$ ]] && (( 10#$configured_trusted_proxy_hops >= 1 )); then
    ok "trusted proxy hops configured: $configured_trusted_proxy_hops"
  else
    fail "server.trusted_proxy_hops should be at least 1 behind a public reverse proxy, got: ${configured_trusted_proxy_hops:-<unset>}"
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

  if [[ -f "$STORAGE_ROOT/.mnemonas/initial-password.txt" ]]; then
    fail "initial admin password file still exists; change the admin password before public access"
  fi

  note "manual cloud firewall check: expose only 80/443 publicly; keep $SERVER_PORT/$DATAPLANE_GRPC_PORT/$DATAPLANE_HTTP_PORT closed to the public internet"
}

printf 'MnemoNAS deployment doctor\n'
printf 'Config: %s\n' "$CONFIG_PATH"
printf 'Storage: %s\n' "$STORAGE_ROOT"
printf '\n'

check_file "$BIN_DIR/nasd" "nasd binary"
check_file "$BIN_DIR/dataplane" "dataplane binary"
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

if [[ -f "$STORAGE_ROOT/.mnemonas/initial-password.txt" ]]; then
  warn "initial admin password file still exists; log in once and change the password"
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
