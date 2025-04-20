#!/usr/bin/env bash

set -euo pipefail

SERVICE_USER="${SERVICE_USER:-mnemonas}"
SERVICE_GROUP="${SERVICE_GROUP:-$SERVICE_USER}"
PREFIX="${PREFIX:-/usr/local}"
BIN_DIR="${BIN_DIR:-$PREFIX/bin}"
SHARE_DIR="${SHARE_DIR:-$PREFIX/share/mnemonas}"
CONFIG_DIR="${CONFIG_DIR:-/etc/mnemonas}"
SYSTEMD_DIR="${SYSTEMD_DIR:-/etc/systemd/system}"
STORAGE_ROOT="${STORAGE_ROOT:-/srv/mnemonas}"
REMOVE_CONFIG="${REMOVE_CONFIG:-0}"
REMOVE_DATA="${REMOVE_DATA:-0}"
REMOVE_SERVICE_USER="${REMOVE_SERVICE_USER:-0}"
CONFIRM_REMOVE_DATA="${CONFIRM_REMOVE_DATA:-}"

log() {
  printf '[mnemonas-uninstall] %s\n' "$*"
}

fail() {
  printf '[mnemonas-uninstall] ERROR: %s\n' "$*" >&2
  exit 1
}

require_root() {
  [[ "$(id -u)" -eq 0 ]] || fail "run this uninstaller as root, for example: sudo $0"
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

require_absolute_path() {
  local value="$1"
  local label="$2"
  [[ "$value" == /* ]] || fail "$label must be an absolute path: $value"
  [[ "$value" != *[[:space:]]* ]] || fail "$label cannot contain whitespace: $value"
}

require_safe_remove_path() {
  local value="$1"
  local label="$2"
  require_absolute_path "$value" "$label"
  [[ "$value" != "/" ]] || fail "$label cannot be /"
}

require_removable_tree_path() {
  local value="$1"
  local label="$2"
  require_safe_remove_path "$value" "$label"
  case "$value" in
    /bin|/boot|/dev|/etc|/home|/lib|/lib64|/media|/mnt|/opt|/proc|/root|/run|/sbin|/srv|/sys|/tmp|/usr|/usr/local|/usr/local/bin|/usr/local/share|/var)
      fail "$label points at a protected system directory and will not be removed: $value"
      ;;
  esac
}

stop_disable_remove_unit() {
  local unit="$1"
  local unit_path="$SYSTEMD_DIR/$unit"

  if [[ ! -f "$unit_path" ]]; then
    log "systemd unit not installed: $unit_path"
    return 0
  fi

  log "stopping and disabling $unit"
  systemctl stop "$unit" >/dev/null 2>&1 || true
  systemctl disable "$unit" >/dev/null 2>&1 || true
  rm -f "$unit_path"
}

remove_installed_file() {
  local path="$1"
  if [[ -e "$path" || -L "$path" ]]; then
    log "removing $path"
    rm -f "$path"
  fi
}

remove_installed_dir() {
  local path="$1"
  local label="$2"
  if [[ -d "$path" ]]; then
    require_removable_tree_path "$path" "$label"
    log "removing $path"
    rm -rf "$path"
  fi
}

remove_service_account() {
  if [[ "$REMOVE_SERVICE_USER" != "1" ]]; then
    log "leaving service account unchanged; set REMOVE_SERVICE_USER=1 to remove it"
    return 0
  fi

  if command -v userdel >/dev/null 2>&1 && id -u "$SERVICE_USER" >/dev/null 2>&1; then
    log "removing service user $SERVICE_USER"
    userdel "$SERVICE_USER" || log "warning: could not remove service user $SERVICE_USER"
  fi
  if command -v groupdel >/dev/null 2>&1 && getent group "$SERVICE_GROUP" >/dev/null 2>&1; then
    log "removing service group $SERVICE_GROUP"
    groupdel "$SERVICE_GROUP" || log "warning: could not remove service group $SERVICE_GROUP"
  fi
}

main() {
  require_root
  require_command systemctl
  require_safe_remove_path "$BIN_DIR" "BIN_DIR"
  require_safe_remove_path "$SHARE_DIR" "SHARE_DIR"
  require_safe_remove_path "$CONFIG_DIR" "CONFIG_DIR"
  require_safe_remove_path "$SYSTEMD_DIR" "SYSTEMD_DIR"
  require_safe_remove_path "$STORAGE_ROOT" "STORAGE_ROOT"

  if [[ "$REMOVE_DATA" == "1" && "$CONFIRM_REMOVE_DATA" != "$STORAGE_ROOT" ]]; then
    fail "refusing to remove data; set CONFIRM_REMOVE_DATA=$STORAGE_ROOT together with REMOVE_DATA=1"
  fi
  [[ ! -d "$SHARE_DIR" ]] || require_removable_tree_path "$SHARE_DIR" "SHARE_DIR"
  [[ "$REMOVE_CONFIG" != "1" ]] || require_removable_tree_path "$CONFIG_DIR" "CONFIG_DIR"
  [[ "$REMOVE_DATA" != "1" ]] || require_removable_tree_path "$STORAGE_ROOT" "STORAGE_ROOT"

  stop_disable_remove_unit mnemonas.service
  stop_disable_remove_unit mnemonas-dataplane.service
  systemctl daemon-reload >/dev/null 2>&1 || true
  systemctl reset-failed mnemonas.service mnemonas-dataplane.service >/dev/null 2>&1 || true

  remove_installed_file "$BIN_DIR/nasd"
  remove_installed_file "$BIN_DIR/dataplane"
  remove_installed_file "$BIN_DIR/mnemonas-dataplane-start"
  remove_installed_file "$BIN_DIR/mnemonas-doctor"
  remove_installed_file "$BIN_DIR/mnemonas-uninstall-systemd"
  remove_installed_dir "$SHARE_DIR" "SHARE_DIR"

  if [[ "$REMOVE_CONFIG" == "1" ]]; then
    remove_installed_dir "$CONFIG_DIR" "CONFIG_DIR"
  else
    log "preserving config: $CONFIG_DIR"
  fi

  if [[ "$REMOVE_DATA" == "1" ]]; then
    remove_installed_dir "$STORAGE_ROOT" "STORAGE_ROOT"
  else
    log "preserving data: $STORAGE_ROOT"
  fi

  remove_service_account
  log "uninstalled systemd services"
}

main "$@"
