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

require_absolute_path() {
  local value="$1"
  local label="$2"
  [[ "$value" == /* ]] || fail "$label must be an absolute path: $value"
  [[ "$value" != *[[:space:]]* ]] || fail "$label cannot contain whitespace: $value"
  [[ "$value" != *[[:cntrl:]]* ]] || fail "$label cannot contain control characters: $value"
  ! path_has_parent_segment "$value" || fail "$label cannot contain parent directory segments: $value"
}

require_safe_account_name() {
  local value="$1"
  local label="$2"
  [[ -n "$value" ]] || fail "$label cannot be empty"
  [[ "$value" != *[[:space:]]* ]] || fail "$label cannot contain whitespace: $value"
  [[ "$value" != *$'\n'* && "$value" != *$'\r'* ]] || fail "$label cannot contain newline characters: $value"
  [[ "$value" != *[[:cntrl:]]* ]] || fail "$label cannot contain control characters: $value"
  [[ "$value" != *%* ]] || fail "$label cannot contain systemd specifiers (%): $value"
  [[ "$value" != *\"* && "$value" != *\\* ]] || fail "$label cannot contain quote or backslash characters: $value"
  [[ "$value" != "root" ]] || fail "$label must not be root"
  [[ "$value" =~ ^[A-Za-z_][A-Za-z0-9_-]{0,63}\$?$ ]] || fail "$label must be a plain system account name: $value"
}

require_safe_remove_path() {
  local value="$1"
  local label="$2"
  require_absolute_path "$value" "$label"
  [[ "$(normalize_absolute_path "$value")" != "/" ]] || fail "$label cannot be /"
  require_no_symlink_components "$value" "$label"
}

require_removable_tree_path() {
  local value="$1"
  local label="$2"
  local normalized
  require_safe_remove_path "$value" "$label"
  normalized="$(normalize_absolute_path "$value")"
  case "$normalized" in
    /bin|/boot|/dev|/etc|/home|/lib|/lib64|/media|/mnt|/opt|/proc|/root|/run|/sbin|/srv|/sys|/tmp|/usr|/usr/local|/usr/local/bin|/usr/local/share|/var)
      fail "$label points at a protected system directory and will not be removed: $value"
      ;;
	  esac
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

require_uninstall_path_layout() {
  local label

  for label in BIN_DIR CONFIG_DIR SYSTEMD_DIR STORAGE_ROOT; do
    require_no_path_overlap "$SHARE_DIR" "SHARE_DIR" "$label"
  done

  if [[ "$REMOVE_CONFIG" == "1" ]]; then
    for label in BIN_DIR SYSTEMD_DIR STORAGE_ROOT; do
      require_no_path_overlap "$CONFIG_DIR" "CONFIG_DIR" "$label"
    done
  fi

  if [[ "$REMOVE_DATA" == "1" ]]; then
    for label in BIN_DIR CONFIG_DIR SYSTEMD_DIR; do
      require_no_path_overlap "$STORAGE_ROOT" "STORAGE_ROOT" "$label"
    done
  fi
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
  rm -f -- "$unit_path"
}

remove_installed_file() {
  local path="$1"
  if [[ -e "$path" || -L "$path" ]]; then
    log "removing $path"
    rm -f -- "$path"
  fi
}

remove_installed_dir() {
  local path="$1"
  local label="$2"
  if [[ -d "$path" ]]; then
    require_removable_tree_path "$path" "$label"
    log "removing $path"
    rm -rf -- "$path"
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
  require_safe_account_name "$SERVICE_USER" "SERVICE_USER"
  require_safe_account_name "$SERVICE_GROUP" "SERVICE_GROUP"
  require_safe_remove_path "$BIN_DIR" "BIN_DIR"
  require_safe_remove_path "$SHARE_DIR" "SHARE_DIR"
  require_safe_remove_path "$CONFIG_DIR" "CONFIG_DIR"
  require_safe_remove_path "$SYSTEMD_DIR" "SYSTEMD_DIR"
  require_safe_remove_path "$STORAGE_ROOT" "STORAGE_ROOT"
  require_uninstall_path_layout

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
  remove_installed_file "$BIN_DIR/mnemonas-public-setup"
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
