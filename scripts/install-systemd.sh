#!/usr/bin/env bash

set -euo pipefail

STORAGE_ROOT_SET="${STORAGE_ROOT+x}"
SERVER_HOST_SET="${SERVER_HOST+x}"
SERVER_PORT_SET="${SERVER_PORT+x}"
DATAPLANE_GRPC_ADDR_SET="${DATAPLANE_GRPC_ADDR+x}"

SERVICE_USER="${SERVICE_USER:-mnemonas}"
SERVICE_GROUP="${SERVICE_GROUP:-$SERVICE_USER}"
PREFIX="${PREFIX:-/usr/local}"
BIN_DIR="${BIN_DIR:-$PREFIX/bin}"
SHARE_DIR="${SHARE_DIR:-$PREFIX/share/mnemonas}"
WEB_DIR="${WEB_DIR:-$SHARE_DIR/web}"
CONFIG_DIR="${CONFIG_DIR:-/etc/mnemonas}"
CONFIG_PATH="${CONFIG_PATH:-$CONFIG_DIR/config.toml}"
SYSTEMD_DIR="${SYSTEMD_DIR:-/etc/systemd/system}"
STORAGE_ROOT="${STORAGE_ROOT:-/srv/mnemonas}"
SERVER_HOST="${SERVER_HOST:-0.0.0.0}"
SERVER_PORT="${SERVER_PORT:-8080}"
DATAPLANE_GRPC_ADDR="${DATAPLANE_GRPC_ADDR:-127.0.0.1:9090}"
DATAPLANE_HTTP_ADDR="${DATAPLANE_HTTP_ADDR:-127.0.0.1:9091}"
ENABLE_NOW="${ENABLE_NOW:-1}"
FIX_STORAGE_OWNERSHIP="${FIX_STORAGE_OWNERSHIP:-0}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

log() {
  printf '[mnemonas-install] %s\n' "$*"
}

fail() {
  printf '[mnemonas-install] ERROR: %s\n' "$*" >&2
  exit 1
}

shell_quote() {
  printf '%q' "$1"
}

service_user_home() {
  local entry home

  if entry="$(getent passwd "$SERVICE_USER" 2>/dev/null)"; then
    home="$(printf '%s\n' "$entry" | awk -F: '{ print $6; exit }')"
    if [[ -n "$home" ]]; then
      printf '%s\n' "$home"
      return
    fi
  fi
  printf '%s\n' "$STORAGE_ROOT"
}

expand_service_user_path() {
  local value="$1"
  local home

  case "$value" in
    \~)
      service_user_home
      ;;
    \~/*)
      home="$(service_user_home)"
      printf '%s/%s\n' "$home" "${value#\~/}"
      ;;
    *)
      printf '%s\n' "$value"
      ;;
  esac
}

initial_password_file_path() {
  local users_file users_dir

  users_file="$(toml_value auth users_file "$CONFIG_PATH")"
  if [[ -n "$users_file" ]]; then
    users_file="$(expand_service_user_path "$users_file")"
  else
    users_file="$STORAGE_ROOT/.mnemonas/users.json"
  fi
  users_dir="${users_file%/*}"
  if [[ "$users_dir" == "$users_file" ]]; then
    users_dir="."
  fi
  printf '%s/initial-password.txt\n' "$users_dir"
}

require_root() {
  [[ "$(id -u)" -eq 0 ]] || fail "run this installer as root, for example: sudo $0"
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

normalize_absolute_path() {
  local value="$1"
  while [[ "$value" != "/" && "$value" == */ ]]; do
    value="${value%/}"
  done
  printf '%s\n' "$value"
}

path_has_parent_segment() {
  local value="$1"
  local trimmed="${value#/}"
  trimmed="${trimmed%/}"

  local -a segments
  IFS='/' read -r -a segments <<< "$trimmed"

  local segment
  for segment in "${segments[@]}"; do
    [[ "$segment" == ".." ]] && return 0
  done
  return 1
}

require_no_symlink_components() {
  local value="$1"
  local label="$2"
  local trimmed="${value#/}"
  trimmed="${trimmed%/}"
  local current="/"
  local -a segments

  IFS='/' read -r -a segments <<< "$trimmed"
  for segment in "${segments[@]}"; do
    [[ -n "$segment" && "$segment" != "." ]] || continue
    if [[ "$current" == "/" ]]; then
      current="/$segment"
    else
      current="$current/$segment"
    fi
    [[ ! -L "$current" ]] || fail "$label must not contain symlink path components: $current"
    [[ -e "$current" ]] || break
  done
}

is_protected_system_directory() {
  local value
  value="$(normalize_absolute_path "$1")"
  case "$value" in
    /bin|/boot|/dev|/etc|/home|/lib|/lib64|/media|/mnt|/opt|/proc|/root|/run|/sbin|/srv|/sys|/tmp|/usr|/usr/local|/usr/local/bin|/usr/local/share|/var)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

require_absolute_path() {
  local value="$1"
  local label="$2"
  [[ "$value" == /* ]] || fail "$label must be an absolute path for systemd deployment: $value"
  [[ "$value" != *[[:space:]]* ]] || fail "$label cannot contain whitespace for systemd deployment: $value"
  [[ "$value" != *[[:cntrl:]]* ]] || fail "$label cannot contain control characters: $value"
  ! path_has_parent_segment "$value" || fail "$label cannot contain parent directory segments: $value"
}

require_safe_directory_path() {
  local value="$1"
  local label="$2"
  require_absolute_path "$value" "$label"
  [[ "$(normalize_absolute_path "$value")" != "/" ]] || fail "$label cannot be /"
  require_no_symlink_components "$value" "$label"
}

require_mutable_tree_path() {
  local value="$1"
  local label="$2"
  require_safe_directory_path "$value" "$label"
  ! is_protected_system_directory "$value" || fail "$label points at a protected system directory and will not be modified: $value"
}

require_removable_tree_path() {
  local value="$1"
  local label="$2"
  require_safe_directory_path "$value" "$label"
  ! is_protected_system_directory "$value" || fail "$label points at a protected system directory and will not be removed: $value"
}

path_matches_or_contains() {
  local parent child
  parent="$(normalize_absolute_path "$1")"
  child="$(normalize_absolute_path "$2")"
  [[ "$parent" == "$child" ]] && return 0
  [[ "$parent" != "/" && "$child" == "$parent"/* ]]
}

paths_overlap() {
  local left="$1"
  local right="$2"
  path_matches_or_contains "$left" "$right" || path_matches_or_contains "$right" "$left"
}

require_no_path_overlap() {
  local path="$1"
  local label="$2"
  local other_label="$3"
  local other_path="${!other_label}"

  if paths_overlap "$path" "$other_path"; then
    fail "$label must not overlap $other_label: $other_path"
  fi
}

require_core_path_layout() {
  require_no_path_overlap "$BIN_DIR" "BIN_DIR" "CONFIG_DIR"
  require_no_path_overlap "$BIN_DIR" "BIN_DIR" "SYSTEMD_DIR"
  require_no_path_overlap "$BIN_DIR" "BIN_DIR" "STORAGE_ROOT"
  require_no_path_overlap "$CONFIG_DIR" "CONFIG_DIR" "SYSTEMD_DIR"
  require_no_path_overlap "$CONFIG_DIR" "CONFIG_DIR" "STORAGE_ROOT"
  require_no_path_overlap "$SYSTEMD_DIR" "SYSTEMD_DIR" "STORAGE_ROOT"
}

require_safe_share_dir() {
  require_mutable_tree_path "$SHARE_DIR" "SHARE_DIR"

  local label protected_path
  for label in BIN_DIR CONFIG_DIR SYSTEMD_DIR STORAGE_ROOT; do
    protected_path="${!label}"
    if paths_overlap "$SHARE_DIR" "$protected_path"; then
      fail "SHARE_DIR must not overlap $label: $protected_path"
    fi
  done
}

require_safe_web_dir() {
  require_removable_tree_path "$WEB_DIR" "WEB_DIR"

  local label protected_path
  for label in BIN_DIR CONFIG_DIR SYSTEMD_DIR STORAGE_ROOT; do
    protected_path="${!label}"
    if paths_overlap "$WEB_DIR" "$protected_path"; then
      fail "WEB_DIR must not overlap $label: $protected_path"
    fi
  done
}

require_config_path_under_config_dir() {
  local config_dir config_path
  config_dir="$(normalize_absolute_path "$CONFIG_DIR")"
  config_path="$(normalize_absolute_path "$CONFIG_PATH")"

  if [[ "$config_path" == "$config_dir" || "$config_path" != "$config_dir"/* ]]; then
    fail "CONFIG_PATH must be inside CONFIG_DIR: $CONFIG_PATH"
  fi
}

require_no_whitespace() {
  local value="$1"
  local label="$2"
  [[ -n "$value" ]] || fail "$label cannot be empty"
  [[ "$value" != *[[:space:]]* ]] || fail "$label cannot contain whitespace: $value"
}

require_systemd_literal() {
  local value="$1"
  local label="$2"
  [[ "$value" != *$'\n'* && "$value" != *$'\r'* ]] || fail "$label cannot contain newline characters: $value"
  [[ "$value" != *[[:cntrl:]]* ]] || fail "$label cannot contain control characters: $value"
  [[ "$value" != *%* ]] || fail "$label cannot contain systemd specifiers (%): $value"
  [[ "$value" != *\"* && "$value" != *\\* ]] || fail "$label cannot contain quote or backslash characters for systemd deployment: $value"
}

require_safe_account_name() {
  local value="$1"
  local label="$2"
  require_no_whitespace "$value" "$label"
  require_systemd_literal "$value" "$label"
  [[ "$value" != "root" ]] || fail "$label must not be root"
  [[ "$value" =~ ^[A-Za-z_][A-Za-z0-9_-]{0,63}\$?$ ]] || fail "$label must be a plain system account name: $value"
}

endpoint_host() {
  local endpoint="$1"
  local host="$endpoint"
  if [[ "$host" == *:* ]]; then
    host="${host%:*}"
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

warn_if_non_loopback_endpoint() {
  local endpoint="$1"
  local label="$2"
  local host
  host="$(endpoint_host "$endpoint")"
  if ! is_loopback_host "$host"; then
    log "warning: $label is $endpoint; dataplane ports do not provide external authentication and should stay on 127.0.0.1 unless isolated by a trusted private network"
  fi
}

require_tcp_port() {
  local value="$1"
  local label="$2"
  require_no_whitespace "$value" "$label"
  [[ "$value" =~ ^[0-9]+$ ]] || fail "$label must be a numeric TCP port: $value"
  (( 10#$value >= 1 && 10#$value <= 65535 )) || fail "$label must be between 1 and 65535: $value"
}

normalize_tcp_port() {
  local value="$1"
  printf '%s\n' "$((10#$value))"
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

normalize_listen_host() {
  local host="$1"
  if [[ "$host" == "*" ]]; then
    printf '\n'
    return 0
  fi
  if [[ "$host" =~ ^\[([^][]+)\]$ ]]; then
    printf '%s\n' "${BASH_REMATCH[1]}"
    return 0
  fi
  printf '%s\n' "$host"
}

require_safe_listen_host() {
  local value="$1"
  local label="$2"
  local host

  [[ "$value" != *[[:space:]]* ]] || fail "$label cannot contain whitespace: $value"
  host="$(normalize_listen_host "$value")"
  if [[ -n "$host" && ( "$host" == *"["* || "$host" == *"]"* ) ]]; then
    fail "$label must not include brackets unless it is a bracketed IPv6 literal: $value"
  fi
  [[ -z "$host" ]] || is_valid_tcp_host "$host" || fail "$label must be empty, *, a hostname, IPv4, or IPv6 literal without a port: $value"
}

require_safe_tcp_addr() {
  local value="$1"
  local label="$2"
  local host=""
  local port=""

  require_no_whitespace "$value" "$label"

  if [[ "$value" =~ ^\[([^][]+)\]:([0-9]+)$ ]]; then
    host="${BASH_REMATCH[1]}"
    port="${BASH_REMATCH[2]}"
  elif [[ "$value" =~ ^([^:]+):([0-9]+)$ ]]; then
    host="${BASH_REMATCH[1]}"
    port="${BASH_REMATCH[2]}"
  else
    fail "$label must be a host:port address: $value"
  fi

  is_valid_tcp_host "$host" || fail "$label host is invalid: $value"
  (( 10#$port >= 1 && 10#$port <= 65535 )) || fail "$label port must be between 1 and 65535: $value"
}

sed_replacement_escape() {
  printf '%s' "$1" | sed -e 's/[&|\\]/\\&/g'
}

toml_basic_string_escape() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '%s' "$value"
}

first_existing_file() {
  local candidate
  for candidate in "$@"; do
    if [[ -f "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

first_built_web_dir() {
  local candidate
  for candidate in "$@"; do
    if [[ -f "$candidate/index.html" && -d "$candidate/assets" ]] && ! grep -q 'src/main\.tsx' "$candidate/index.html"; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

has_newer_file_than() {
  local reference="$1"
  shift

  local candidate found
  for candidate in "$@"; do
    [[ -e "$candidate" ]] || continue
    if [[ -f "$candidate" ]]; then
      [[ "$candidate" -nt "$reference" ]] && return 0
      continue
    fi

    found="$(find "$candidate" -type f -newer "$reference" \
      ! -path '*/.git/*' \
      ! -path '*/node_modules/*' \
      ! -path '*/dist/*' \
      ! -path '*/target/*' \
      -print -quit)"
    [[ -n "$found" ]] && return 0
  done

  return 1
}

require_checkout_artifacts_current() {
  local release_root="$1"
  local nasd_src="$2"
  local dataplane_src="$3"
  local web_src="$4"

  [[ -d "$release_root/.git" ]] || return 0

  if has_newer_file_than "$nasd_src" \
    "$release_root/cmd" \
    "$release_root/internal" \
    "$release_root/proto" \
    "$release_root/go.mod" \
    "$release_root/go.sum"; then
    fail "nasd binary is older than Go sources; run make build before installing from a source checkout"
  fi

  if has_newer_file_than "$dataplane_src" \
    "$release_root/dataplane/src" \
    "$release_root/dataplane/Cargo.toml" \
    "$release_root/dataplane/Cargo.lock" \
    "$release_root/proto"; then
    fail "dataplane binary is older than Rust/proto sources; run make build before installing from a source checkout"
  fi

  if has_newer_file_than "$web_src/index.html" \
    "$release_root/web/src" \
    "$release_root/web/public" \
    "$release_root/web/package.json" \
    "$release_root/web/package-lock.json" \
    "$release_root/web/vite.config.ts" \
    "$release_root/web/tsconfig.json" \
    "$release_root/web/tsconfig.app.json"; then
    fail "Web UI assets are older than frontend sources; run make build before installing from a source checkout"
  fi
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

apply_existing_config_defaults() {
  [[ -f "$CONFIG_PATH" ]] || return 0

  local value
  if [[ -z "$STORAGE_ROOT_SET" ]]; then
    value="$(toml_value storage root "$CONFIG_PATH")"
    [[ -n "$value" ]] && STORAGE_ROOT="$value"
  fi
  if [[ -z "$SERVER_HOST_SET" ]]; then
    value="$(toml_value server host "$CONFIG_PATH")"
    [[ -n "$value" ]] && SERVER_HOST="$value"
  fi
  if [[ -z "$SERVER_PORT_SET" ]]; then
    value="$(toml_value server port "$CONFIG_PATH")"
    [[ -n "$value" ]] && SERVER_PORT="$value"
  fi
  if [[ -z "$DATAPLANE_GRPC_ADDR_SET" ]]; then
    value="$(toml_value dataplane grpc_address "$CONFIG_PATH")"
    [[ -n "$value" ]] && DATAPLANE_GRPC_ADDR="$value"
  fi
}

resolve_release_root() {
  if [[ -n "${RELEASE_DIR:-}" ]]; then
    cd "$RELEASE_DIR" && pwd
    return 0
  fi

  local parent
  parent="$(cd "$SCRIPT_DIR/.." && pwd)"
  if [[ -f "$PWD/nasd" && -f "$PWD/dataplane" ]]; then
    pwd
    return 0
  fi
  if [[ -f "$parent/nasd" && -f "$parent/dataplane" ]]; then
    printf '%s\n' "$parent"
    return 0
  fi
  pwd
}

create_service_user() {
  if ! getent group "$SERVICE_GROUP" >/dev/null 2>&1; then
    log "creating system group $SERVICE_GROUP"
    groupadd --system "$SERVICE_GROUP"
  fi
  if id -u "$SERVICE_USER" >/dev/null 2>&1; then
    return 0
  fi
  log "creating system user $SERVICE_USER"
  useradd --system --gid "$SERVICE_GROUP" --home "$STORAGE_ROOT" --shell /usr/sbin/nologin "$SERVICE_USER"
}

ensure_storage_directories() {
	log "creating directories"
	mkdir -p "$STORAGE_ROOT/files" "$STORAGE_ROOT/.mnemonas/objects"
	chown "$SERVICE_USER:$SERVICE_GROUP" "$STORAGE_ROOT" "$STORAGE_ROOT/files" "$STORAGE_ROOT/.mnemonas" "$STORAGE_ROOT/.mnemonas/objects"
	chmod 0750 "$STORAGE_ROOT" "$STORAGE_ROOT/files"
	chmod 0700 "$STORAGE_ROOT/.mnemonas" "$STORAGE_ROOT/.mnemonas/objects"

  if [[ "$FIX_STORAGE_OWNERSHIP" == "1" ]]; then
    log "recursively fixing storage ownership under $STORAGE_ROOT"
    chown -R "$SERVICE_USER:$SERVICE_GROUP" "$STORAGE_ROOT"
  else
    log "leaving existing storage contents ownership unchanged; set FIX_STORAGE_OWNERSHIP=1 to repair recursively"
  fi
}

render_config() {
  local template="$1"
  CONFIG_CREATED=0
  mkdir -p "$CONFIG_DIR"
  chown "$SERVICE_USER:$SERVICE_GROUP" "$CONFIG_DIR"
  chmod 0750 "$CONFIG_DIR"
  if [[ -f "$CONFIG_PATH" ]]; then
    log "keeping existing config: $CONFIG_PATH"
    chown "$SERVICE_USER:$SERVICE_GROUP" "$CONFIG_PATH"
    chmod 0600 "$CONFIG_PATH"
    return 0
  fi

  log "creating config: $CONFIG_PATH"
  local server_host_escaped storage_root_escaped dataplane_grpc_addr_escaped
  server_host_escaped="$(sed_replacement_escape "$(toml_basic_string_escape "$SERVER_HOST")")"
  storage_root_escaped="$(sed_replacement_escape "$(toml_basic_string_escape "$STORAGE_ROOT")")"
  dataplane_grpc_addr_escaped="$(sed_replacement_escape "$(toml_basic_string_escape "$DATAPLANE_GRPC_ADDR")")"
  sed \
    -e "s|^host = \".*\"|host = \"$server_host_escaped\"|" \
    -e "s|^port = .*|port = $SERVER_PORT|" \
    -e "s|^root = \".*\"|root = \"$storage_root_escaped\"|" \
    -e "s|^grpc_address = \".*\"|grpc_address = \"$dataplane_grpc_addr_escaped\"|" \
  "$template" > "$CONFIG_PATH"
  chown "$SERVICE_USER:$SERVICE_GROUP" "$CONFIG_PATH"
  chmod 0600 "$CONFIG_PATH"
  CONFIG_CREATED=1
}

cleanup_created_config() {
  if [[ "${CONFIG_CREATED:-0}" == "1" ]]; then
    rm -f -- "$CONFIG_PATH"
  fi
}

install_units() {
  log "installing systemd units"
  mkdir -p "$SYSTEMD_DIR"
  cat > "$SYSTEMD_DIR/mnemonas-dataplane.service" <<EOF
[Unit]
Description=MnemoNAS data plane
Documentation=https://github.com/seanbao/mnemonas
After=local-fs.target
RequiresMountsFor=$STORAGE_ROOT

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_GROUP
Environment=CONFIG_PATH=$CONFIG_PATH
Environment=DATAPLANE_BIN=$BIN_DIR/dataplane
Environment=DATAPLANE_DATA_DIR=$STORAGE_ROOT/.mnemonas/objects
Environment=DATAPLANE_HTTP_ADDR=$DATAPLANE_HTTP_ADDR
Environment=DATAPLANE_GRPC_ADDR=$DATAPLANE_GRPC_ADDR
ExecStart=$BIN_DIR/mnemonas-dataplane-start
Restart=on-failure
RestartSec=5
LimitNOFILE=1048576
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=full
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
CapabilityBoundingSet=
AmbientCapabilities=
SystemCallArchitectures=native
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
ReadWritePaths=$STORAGE_ROOT

[Install]
WantedBy=multi-user.target
EOF

  cat > "$SYSTEMD_DIR/mnemonas.service" <<EOF
[Unit]
Description=MnemoNAS control plane and Web UI
Documentation=https://github.com/seanbao/mnemonas
Requires=mnemonas-dataplane.service
After=network-online.target mnemonas-dataplane.service
Wants=network-online.target
RequiresMountsFor=$STORAGE_ROOT

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_GROUP
Environment=MNEMONAS_WEB_DIR=$WEB_DIR
Environment=DATAPLANE_HTTP_ADDR=$DATAPLANE_HTTP_ADDR
ExecStart=$BIN_DIR/nasd --config $CONFIG_PATH
Restart=on-failure
RestartSec=5
LimitNOFILE=1048576
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=full
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
CapabilityBoundingSet=
AmbientCapabilities=
SystemCallArchitectures=native
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
ReadWritePaths=$STORAGE_ROOT $CONFIG_DIR

[Install]
WantedBy=multi-user.target
EOF
}

install_web_assets() {
  local web_src="$1"
  local web_parent web_name tmp_dir backup_parent backup_path

  web_parent="$(dirname "$WEB_DIR")"
  web_name="$(basename "$WEB_DIR")"
  mkdir -p "$SHARE_DIR" "$web_parent"
  chmod a+x "$SHARE_DIR"

  tmp_dir="$(mktemp -d "$web_parent/.${web_name}.new.XXXXXXXX")"
  if ! cp -a "$web_src"/. "$tmp_dir"/; then
    rm -rf -- "$tmp_dir"
    return 1
  fi
  if ! chmod -R a+rX "$tmp_dir"; then
    rm -rf -- "$tmp_dir"
    return 1
  fi

  backup_parent=""
  backup_path=""
  if [[ -e "$WEB_DIR" || -L "$WEB_DIR" ]]; then
    backup_parent="$(mktemp -d "$web_parent/.${web_name}.old.XXXXXXXX")"
    backup_path="$backup_parent/$web_name"
    if ! mv -- "$WEB_DIR" "$backup_path"; then
      rm -rf -- "$tmp_dir" "$backup_parent"
      return 1
    fi
  fi

  if ! mv -- "$tmp_dir" "$WEB_DIR"; then
    if [[ -n "$backup_path" && -e "$backup_path" ]]; then
      mv -- "$backup_path" "$WEB_DIR" || true
    fi
    rm -rf -- "$tmp_dir" "$backup_parent"
    return 1
  fi

  if [[ -n "$backup_parent" ]]; then
    rm -rf -- "$backup_parent"
  fi
}

install_binary_assets() {
  local nasd_src="$1"
  local dataplane_src="$2"
  local dataplane_start_src="$3"
  local doctor_src="$4"
  local public_setup_src="$5"
  local uninstall_src="$6"
  local staging_dir backup_dir name target
  local -a names sources backed_names installed_names

  mkdir -p "$BIN_DIR"
  staging_dir="$(mktemp -d "$BIN_DIR/.mnemonas-bin.new.XXXXXXXX")"
  names=(nasd dataplane mnemonas-dataplane-start)
  sources=("$nasd_src" "$dataplane_src" "$dataplane_start_src")
  if [[ -n "$doctor_src" ]]; then
    names+=(mnemonas-doctor)
    sources+=("$doctor_src")
  fi
  if [[ -n "$public_setup_src" ]]; then
    names+=(mnemonas-public-setup)
    sources+=("$public_setup_src")
  fi
  if [[ -n "$uninstall_src" ]]; then
    names+=(mnemonas-uninstall-systemd)
    sources+=("$uninstall_src")
  fi

  local i
  for i in "${!names[@]}"; do
    if ! install -m 0755 "${sources[$i]}" "$staging_dir/${names[$i]}"; then
      rm -rf -- "$staging_dir"
      return 1
    fi
  done

  backup_dir="$(mktemp -d "$BIN_DIR/.mnemonas-bin.old.XXXXXXXX")"
  for name in "${names[@]}"; do
    target="$BIN_DIR/$name"
    if [[ -e "$target" || -L "$target" ]]; then
      if ! mv -- "$target" "$backup_dir/$name"; then
        local backed_name
        for backed_name in "${backed_names[@]}"; do
          mv -- "$backup_dir/$backed_name" "$BIN_DIR/$backed_name" || true
        done
        rm -rf -- "$staging_dir" "$backup_dir"
        return 1
      fi
      backed_names+=("$name")
    fi
  done

  for name in "${names[@]}"; do
    if ! mv -- "$staging_dir/$name" "$BIN_DIR/$name"; then
      local installed_name
      for installed_name in "${installed_names[@]}"; do
        rm -f -- "$BIN_DIR/$installed_name"
      done
      local backed_name
      for backed_name in "${backed_names[@]}"; do
        mv -- "$backup_dir/$backed_name" "$BIN_DIR/$backed_name" || true
      done
      rm -rf -- "$staging_dir" "$backup_dir"
      return 1
    fi
    installed_names+=("$name")
  done

  rm -rf -- "$staging_dir" "$backup_dir"
}

detect_primary_ipv4() {
  local ip_addr=""

  if command -v ip >/dev/null 2>&1; then
    ip_addr="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{ for (i = 1; i <= NF; i++) if ($i == "src") { print $(i + 1); exit } }')"
  fi
  if [[ -z "$ip_addr" ]] && command -v hostname >/dev/null 2>&1; then
    ip_addr="$(hostname -I 2>/dev/null | awk '{ print $1 }')"
  fi

  printf '%s\n' "$ip_addr"
}

web_ui_url() {
  local host
  host="$(normalize_listen_host "$SERVER_HOST")"
  case "$host" in
    ""|0.0.0.0|::)
      host="$(detect_primary_ipv4)"
      [[ -n "$host" ]] || host="<ubuntu-laptop-ip>"
      ;;
  esac
  if [[ "$host" == *:* && "$host" != \[*\] ]]; then
    host="[$host]"
  fi

  printf 'http://%s:%s\n' "$host" "$SERVER_PORT"
}

restart_service() {
  local service="$1"

  if systemctl restart "$service"; then
    return 0
  fi

  fail "failed to restart $service; inspect with: systemctl status $service --no-pager; journalctl -u $service -n 100 --no-pager"
}

reload_systemd_units() {
  if systemctl daemon-reload; then
    return 0
  fi

  fail "failed to reload systemd units; inspect unit files with: systemctl cat mnemonas.service mnemonas-dataplane.service; systemctl status mnemonas.service --no-pager"
}

enable_services() {
  if systemctl enable mnemonas-dataplane.service mnemonas.service >/dev/null; then
    return 0
  fi

  fail "failed to enable systemd units; inspect with: systemctl status mnemonas.service --no-pager; systemctl status mnemonas-dataplane.service --no-pager; journalctl -u mnemonas.service -u mnemonas-dataplane.service -n 100 --no-pager"
}

main() {
  require_root
  require_command install
  require_command grep
  require_command mktemp
  require_command sed
  require_command systemctl

  local release_root nasd_src dataplane_src dataplane_start_src doctor_src public_setup_src uninstall_src web_src config_template
  release_root="$(resolve_release_root)"
  nasd_src="$(first_existing_file "$release_root/nasd" "$release_root/bin/nasd" "$PWD/nasd" "$PWD/bin/nasd")" || fail "nasd binary not found; run from a release tarball or set RELEASE_DIR"
  dataplane_src="$(first_existing_file "$release_root/dataplane" "$release_root/bin/dataplane" "$PWD/dataplane" "$PWD/bin/dataplane")" || fail "dataplane binary not found; run from a release tarball or set RELEASE_DIR"
  dataplane_start_src="$(first_existing_file "$release_root/scripts/mnemonas-dataplane-start.sh" "$SCRIPT_DIR/mnemonas-dataplane-start.sh" "$PWD/scripts/mnemonas-dataplane-start.sh")" || fail "mnemonas-dataplane-start.sh not found"
  doctor_src="$(first_existing_file "$release_root/scripts/mnemonas-doctor.sh" "$SCRIPT_DIR/mnemonas-doctor.sh" "$PWD/scripts/mnemonas-doctor.sh" 2>/dev/null || true)"
  public_setup_src="$(first_existing_file "$release_root/scripts/setup-reverse-proxy.sh" "$SCRIPT_DIR/setup-reverse-proxy.sh" "$PWD/scripts/setup-reverse-proxy.sh" 2>/dev/null || true)"
  uninstall_src="$(first_existing_file "$release_root/scripts/uninstall-systemd.sh" "$SCRIPT_DIR/uninstall-systemd.sh" "$PWD/scripts/uninstall-systemd.sh" 2>/dev/null || true)"
  web_src="$(first_built_web_dir "$release_root/web/dist" "$release_root/web" "$PWD/web/dist" "$PWD/web")" || fail "built web assets not found; install from a release package or run npm run build in web/"
  config_template="$(first_existing_file "$release_root/mnemonas.example.toml" "$PWD/mnemonas.example.toml")" || fail "mnemonas.example.toml not found"

  require_checkout_artifacts_current "$release_root" "$nasd_src" "$dataplane_src" "$web_src"

  apply_existing_config_defaults
  require_safe_directory_path "$BIN_DIR" "BIN_DIR"
  require_mutable_tree_path "$CONFIG_DIR" "CONFIG_DIR"
  require_absolute_path "$CONFIG_PATH" "CONFIG_PATH"
  require_no_symlink_components "$CONFIG_PATH" "CONFIG_PATH"
  require_config_path_under_config_dir
  require_safe_directory_path "$SYSTEMD_DIR" "SYSTEMD_DIR"
  require_mutable_tree_path "$STORAGE_ROOT" "STORAGE_ROOT"
  require_mutable_tree_path "$STORAGE_ROOT/files" "storage files directory"
  require_mutable_tree_path "$STORAGE_ROOT/.mnemonas/objects" "storage internal object directory"
  require_core_path_layout
  require_safe_share_dir
  require_safe_web_dir
  require_safe_account_name "$SERVICE_USER" "SERVICE_USER"
  require_safe_account_name "$SERVICE_GROUP" "SERVICE_GROUP"
  require_systemd_literal "$BIN_DIR" "BIN_DIR"
  require_systemd_literal "$CONFIG_DIR" "CONFIG_DIR"
  require_systemd_literal "$CONFIG_PATH" "CONFIG_PATH"
  require_systemd_literal "$STORAGE_ROOT" "STORAGE_ROOT"
  require_systemd_literal "$WEB_DIR" "WEB_DIR"
  require_systemd_literal "$DATAPLANE_GRPC_ADDR" "DATAPLANE_GRPC_ADDR"
  require_systemd_literal "$DATAPLANE_HTTP_ADDR" "DATAPLANE_HTTP_ADDR"
  require_safe_listen_host "$SERVER_HOST" "SERVER_HOST"
  require_tcp_port "$SERVER_PORT" "SERVER_PORT"
  SERVER_PORT="$(normalize_tcp_port "$SERVER_PORT")"
  require_safe_tcp_addr "$DATAPLANE_GRPC_ADDR" "DATAPLANE_GRPC_ADDR"
  require_safe_tcp_addr "$DATAPLANE_HTTP_ADDR" "DATAPLANE_HTTP_ADDR"
  warn_if_non_loopback_endpoint "$DATAPLANE_GRPC_ADDR" "DATAPLANE_GRPC_ADDR"
  warn_if_non_loopback_endpoint "$DATAPLANE_HTTP_ADDR" "DATAPLANE_HTTP_ADDR"
  create_service_user

  ensure_storage_directories
  render_config "$config_template"
  if ! "$nasd_src" --check-config --config "$CONFIG_PATH" >/dev/null; then
    cleanup_created_config
    return 1
  fi

  mkdir -p "$BIN_DIR"
  log "installing binaries"
  install_binary_assets "$nasd_src" "$dataplane_src" "$dataplane_start_src" "$doctor_src" "$public_setup_src" "$uninstall_src"

  log "installing Web UI assets"
  install_web_assets "$web_src"

  install_units
  reload_systemd_units
  enable_services

  if [[ "$ENABLE_NOW" != "0" ]]; then
    log "starting services"
    restart_service mnemonas-dataplane.service
    restart_service mnemonas.service
  fi

  local initial_password_file web_url
  local quoted_initial_password_file quoted_doctor_path quoted_public_setup_path quoted_uninstall_path
  initial_password_file="$(initial_password_file_path)"
  web_url="$(web_ui_url)"
  quoted_initial_password_file="$(shell_quote "$initial_password_file")"
  quoted_doctor_path="$(shell_quote "$BIN_DIR/mnemonas-doctor")"
  quoted_public_setup_path="$(shell_quote "$BIN_DIR/mnemonas-public-setup")"
  quoted_uninstall_path="$(shell_quote "$BIN_DIR/mnemonas-uninstall-systemd")"

  log "installed successfully"
  log "Next steps:"
  log "  Open Web UI: $web_url"
  log "  Read initial password: sudo cat $quoted_initial_password_file"
  log "  Run doctor: sudo $quoted_doctor_path"
  log "  Configure public HTTPS: sudo $quoted_public_setup_path --proxy caddy <domain> <email>"
  log "  Check status: systemctl status mnemonas --no-pager"
  log "  View logs: journalctl -u mnemonas -f"
  log "  Keep this release directory; rerun its installer to return to this version after a failed upgrade"
  log "  Uninstall: sudo $quoted_uninstall_path"
}

main "$@"
