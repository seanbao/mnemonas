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

require_root() {
  [[ "$(id -u)" -eq 0 ]] || fail "run this installer as root, for example: sudo $0"
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

require_absolute_path() {
  local value="$1"
  local label="$2"
  [[ "$value" == /* ]] || fail "$label must be an absolute path for systemd deployment: $value"
  [[ "$value" != *[[:space:]]* ]] || fail "$label cannot contain whitespace for systemd deployment: $value"
}

require_safe_directory_path() {
  local value="$1"
  local label="$2"
  require_absolute_path "$value" "$label"
  [[ "$value" != "/" ]] || fail "$label cannot be /"
}

require_no_whitespace() {
  local value="$1"
  local label="$2"
  [[ -n "$value" ]] || fail "$label cannot be empty"
  [[ "$value" != *[[:space:]]* ]] || fail "$label cannot contain whitespace: $value"
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
  (( value >= 1 && value <= 65535 )) || fail "$label must be between 1 and 65535: $value"
}

sed_replacement_escape() {
  printf '%s' "$1" | sed -e 's/[&|\\]/\\&/g'
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

toml_value() {
  local section="$1"
  local key="$2"
  local file="$3"
  awk -v section="[$section]" -v key="$key" '
    {
      line = $0
      sub("[[:space:]]*#.*$", "", line)
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
  server_host_escaped="$(sed_replacement_escape "$SERVER_HOST")"
  storage_root_escaped="$(sed_replacement_escape "$STORAGE_ROOT")"
  dataplane_grpc_addr_escaped="$(sed_replacement_escape "$DATAPLANE_GRPC_ADDR")"
  sed \
    -e "s|^host = \".*\"|host = \"$server_host_escaped\"|" \
    -e "s|^port = .*|port = $SERVER_PORT|" \
    -e "s|^root = \".*\"|root = \"$storage_root_escaped\"|" \
    -e "s|^grpc_address = \".*\"|grpc_address = \"$dataplane_grpc_addr_escaped\"|" \
    "$template" > "$CONFIG_PATH"
  chown "$SERVICE_USER:$SERVICE_GROUP" "$CONFIG_PATH"
  chmod 0600 "$CONFIG_PATH"
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
  local host="$SERVER_HOST"
  case "$host" in
    0.0.0.0|::|\[::\])
      host="$(detect_primary_ipv4)"
      [[ -n "$host" ]] || host="<ubuntu-laptop-ip>"
      ;;
  esac

  printf 'http://%s:%s\n' "$host" "$SERVER_PORT"
}

main() {
  require_root
  require_command install
  require_command grep
  require_command sed
  require_command systemctl

  local release_root nasd_src dataplane_src dataplane_start_src doctor_src uninstall_src web_src config_template
  release_root="$(resolve_release_root)"
  nasd_src="$(first_existing_file "$release_root/nasd" "$release_root/bin/nasd" "$PWD/nasd" "$PWD/bin/nasd")" || fail "nasd binary not found; run from a release tarball or set RELEASE_DIR"
  dataplane_src="$(first_existing_file "$release_root/dataplane" "$release_root/bin/dataplane" "$PWD/dataplane" "$PWD/bin/dataplane")" || fail "dataplane binary not found; run from a release tarball or set RELEASE_DIR"
  dataplane_start_src="$(first_existing_file "$release_root/scripts/mnemonas-dataplane-start.sh" "$SCRIPT_DIR/mnemonas-dataplane-start.sh" "$PWD/scripts/mnemonas-dataplane-start.sh")" || fail "mnemonas-dataplane-start.sh not found"
  doctor_src="$(first_existing_file "$release_root/scripts/mnemonas-doctor.sh" "$SCRIPT_DIR/mnemonas-doctor.sh" "$PWD/scripts/mnemonas-doctor.sh" 2>/dev/null || true)"
  uninstall_src="$(first_existing_file "$release_root/scripts/uninstall-systemd.sh" "$SCRIPT_DIR/uninstall-systemd.sh" "$PWD/scripts/uninstall-systemd.sh" 2>/dev/null || true)"
  web_src="$(first_built_web_dir "$release_root/web/dist" "$release_root/web" "$PWD/web/dist" "$PWD/web")" || fail "built web assets not found; install from a release package or run npm run build in web/"
  config_template="$(first_existing_file "$release_root/mnemonas.example.toml" "$PWD/mnemonas.example.toml")" || fail "mnemonas.example.toml not found"

  apply_existing_config_defaults
  require_safe_directory_path "$BIN_DIR" "BIN_DIR"
  require_safe_directory_path "$WEB_DIR" "WEB_DIR"
  require_safe_directory_path "$CONFIG_DIR" "CONFIG_DIR"
  require_absolute_path "$CONFIG_PATH" "CONFIG_PATH"
  require_safe_directory_path "$SYSTEMD_DIR" "SYSTEMD_DIR"
  require_safe_directory_path "$STORAGE_ROOT" "STORAGE_ROOT"
  require_no_whitespace "$SERVER_HOST" "SERVER_HOST"
  require_tcp_port "$SERVER_PORT" "SERVER_PORT"
  require_no_whitespace "$DATAPLANE_GRPC_ADDR" "DATAPLANE_GRPC_ADDR"
  require_no_whitespace "$DATAPLANE_HTTP_ADDR" "DATAPLANE_HTTP_ADDR"
  warn_if_non_loopback_endpoint "$DATAPLANE_GRPC_ADDR" "DATAPLANE_GRPC_ADDR"
  warn_if_non_loopback_endpoint "$DATAPLANE_HTTP_ADDR" "DATAPLANE_HTTP_ADDR"
  create_service_user

  mkdir -p "$BIN_DIR" "$WEB_DIR"
  ensure_storage_directories

  log "installing binaries"
  install -m 0755 "$nasd_src" "$BIN_DIR/nasd"
  install -m 0755 "$dataplane_src" "$BIN_DIR/dataplane"
  install -m 0755 "$dataplane_start_src" "$BIN_DIR/mnemonas-dataplane-start"
  if [[ -n "$doctor_src" ]]; then
    install -m 0755 "$doctor_src" "$BIN_DIR/mnemonas-doctor"
  fi
  if [[ -n "$uninstall_src" ]]; then
    install -m 0755 "$uninstall_src" "$BIN_DIR/mnemonas-uninstall-systemd"
  fi

  log "installing Web UI assets"
  rm -rf "$WEB_DIR"
  mkdir -p "$WEB_DIR"
  cp -a "$web_src"/. "$WEB_DIR"/
  chmod -R a+rX "$SHARE_DIR"

  render_config "$config_template"
  "$BIN_DIR/nasd" --check-config --config "$CONFIG_PATH" >/dev/null

  install_units
  systemctl daemon-reload
  systemctl enable mnemonas-dataplane.service mnemonas.service >/dev/null

  if [[ "$ENABLE_NOW" != "0" ]]; then
    log "starting services"
    systemctl restart mnemonas-dataplane.service
    systemctl restart mnemonas.service
  fi

  local initial_password_file web_url
  initial_password_file="$STORAGE_ROOT/.mnemonas/initial-password.txt"
  web_url="$(web_ui_url)"

  log "installed successfully"
  log "Next steps:"
  log "  Open Web UI: $web_url"
  log "  Read initial password: sudo cat $initial_password_file"
  log "  Run doctor: sudo $BIN_DIR/mnemonas-doctor"
  log "  Check status: systemctl status mnemonas --no-pager"
  log "  View logs: journalctl -u mnemonas -f"
  log "  Uninstall: sudo $BIN_DIR/mnemonas-uninstall-systemd"
}

main "$@"
