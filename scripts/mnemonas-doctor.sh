#!/usr/bin/env bash

set -u

SERVICE_USER="${SERVICE_USER:-mnemonas}"
BIN_DIR="${BIN_DIR:-/usr/local/bin}"
WEB_DIR="${WEB_DIR:-/usr/local/share/mnemonas/web}"
CONFIG_PATH="${CONFIG_PATH:-/etc/mnemonas/config.toml}"
BACKUP_ROOT="${BACKUP_ROOT:-/backup/mnemonas}"
MIN_FREE_BYTES="${MIN_FREE_BYTES:-10737418240}"

FAILURES=0
WARNINGS=0

ok() {
  printf '[OK] %s\n' "$*"
}

warn() {
  WARNINGS=$((WARNINGS + 1))
  printf '[WARN] %s\n' "$*"
}

fail() {
  FAILURES=$((FAILURES + 1))
  printf '[FAIL] %s\n' "$*"
}

have() {
  command -v "$1" >/dev/null 2>&1
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

configured_storage_root=""
configured_server_port=""
configured_grpc_address=""
if [[ -f "$CONFIG_PATH" ]]; then
  configured_storage_root="$(toml_value storage root "$CONFIG_PATH")"
  configured_server_port="$(toml_value server port "$CONFIG_PATH")"
  configured_grpc_address="$(toml_value dataplane grpc_address "$CONFIG_PATH")"
fi

STORAGE_ROOT="${STORAGE_ROOT:-${configured_storage_root:-/srv/mnemonas}}"
SERVER_PORT="${SERVER_PORT:-${configured_server_port:-8080}}"
DATAPLANE_GRPC_ADDR="${DATAPLANE_GRPC_ADDR:-${configured_grpc_address:-127.0.0.1:9090}}"
DATAPLANE_GRPC_PORT="${DATAPLANE_GRPC_PORT:-${DATAPLANE_GRPC_ADDR##*:}}"
DATAPLANE_HTTP_PORT="${DATAPLANE_HTTP_PORT:-9091}"
SERVER_URL="${SERVER_URL:-http://127.0.0.1:$SERVER_PORT}"
DATAPLANE_URL="${DATAPLANE_URL:-http://127.0.0.1:$DATAPLANE_HTTP_PORT}"

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
  rm -f "$config_check_out"
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
  rm -f "$zpool_out"
fi

if have tailscale; then
  if tailscale status >/dev/null 2>&1; then
    ok "tailscale status is available"
  else
    warn "tailscale is installed but not healthy"
  fi
fi

check_ufw

printf '\nSummary: %d failure(s), %d warning(s)\n' "$FAILURES" "$WARNINGS"
if [[ "$FAILURES" -gt 0 ]]; then
  exit 1
fi
exit 0
