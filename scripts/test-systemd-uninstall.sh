#!/usr/bin/env bash
# shellcheck disable=SC2016

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "$TMP_ROOT"' EXIT

fail() {
  printf '[systemd-uninstall-test] ERROR: %s\n' "$*" >&2
  exit 1
}

write_executable() {
  local path="$1"
  shift
  printf '%s\n' "$@" > "$path"
  chmod +x "$path"
}

make_fake_admin_path() {
  local dir="$1"
  mkdir -p "$dir"
  write_executable "$dir/id" \
    '#!/usr/bin/env bash' \
    'if [[ "$1" == "-u" && "$#" -eq 1 ]]; then printf "0\n"; exit 0; fi' \
    'exit 1'
  write_executable "$dir/systemctl" \
    '#!/usr/bin/env bash' \
    'printf "%s\n" "$*" >> "$SYSTEMCTL_LOG"'
}

make_install_tree() {
  local dir="$1"
  mkdir -p "$dir/bin" "$dir/share/mnemonas/web" "$dir/etc/mnemonas" "$dir/systemd" "$dir/storage/files"
  touch "$dir/bin/nasd" "$dir/bin/dataplane" "$dir/bin/mnemonas-dataplane-start" "$dir/bin/mnemonas-doctor"
  touch "$dir/bin/mnemonas-uninstall-systemd"
  touch "$dir/share/mnemonas/web/index.html"
  touch "$dir/etc/mnemonas/config.toml"
  touch "$dir/systemd/mnemonas.service" "$dir/systemd/mnemonas-dataplane.service"
  touch "$dir/storage/files/keep.txt"
}

assert_exists() {
  local path="$1"
  [[ -e "$path" ]] || fail "$path does not exist"
}

assert_not_exists() {
  local path="$1"
  [[ ! -e "$path" ]] || fail "$path still exists"
}

assert_file_contains() {
  local path="$1"
  local expected="$2"
  grep -Fq -- "$expected" "$path" || fail "$path does not contain: $expected"
}

run_preserve_data_test() {
  local case_dir="$TMP_ROOT/preserve"
  local fake_path="$case_dir/fake-bin"
  local install_dir="$case_dir/install"
  local systemctl_log="$case_dir/systemctl.log"
  make_fake_admin_path "$fake_path"
  make_install_tree "$install_dir"

  PATH="$fake_path:$PATH" \
    SYSTEMCTL_LOG="$systemctl_log" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$install_dir/storage" \
    "$REPO_ROOT/scripts/uninstall-systemd.sh" > "$case_dir/uninstall.log"

  assert_not_exists "$install_dir/bin/nasd"
  assert_not_exists "$install_dir/bin/dataplane"
  assert_not_exists "$install_dir/bin/mnemonas-dataplane-start"
  assert_not_exists "$install_dir/bin/mnemonas-doctor"
  assert_not_exists "$install_dir/bin/mnemonas-uninstall-systemd"
  assert_not_exists "$install_dir/share/mnemonas"
  assert_not_exists "$install_dir/systemd/mnemonas.service"
  assert_not_exists "$install_dir/systemd/mnemonas-dataplane.service"
  assert_exists "$install_dir/etc/mnemonas/config.toml"
  assert_exists "$install_dir/storage/files/keep.txt"
  assert_file_contains "$systemctl_log" "stop mnemonas.service"
  assert_file_contains "$systemctl_log" "disable mnemonas-dataplane.service"
  assert_file_contains "$systemctl_log" "daemon-reload"
  assert_file_contains "$case_dir/uninstall.log" "preserving config"
  assert_file_contains "$case_dir/uninstall.log" "preserving data"
}

run_refuse_unconfirmed_data_removal_test() {
  local case_dir="$TMP_ROOT/refuse-data"
  local fake_path="$case_dir/fake-bin"
  local install_dir="$case_dir/install"
  local systemctl_log="$case_dir/systemctl.log"
  local status
  make_fake_admin_path "$fake_path"
  make_install_tree "$install_dir"

  set +e
  PATH="$fake_path:$PATH" \
    SYSTEMCTL_LOG="$systemctl_log" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$install_dir/storage" \
    REMOVE_DATA=1 \
    CONFIRM_REMOVE_DATA="$install_dir/wrong" \
    "$REPO_ROOT/scripts/uninstall-systemd.sh" > "$case_dir/uninstall.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "uninstaller accepted unconfirmed data removal"
  assert_exists "$install_dir/storage/files/keep.txt"
  assert_exists "$install_dir/systemd/mnemonas.service"
  assert_file_contains "$case_dir/uninstall.log" "refusing to remove data"
}

run_refuse_protected_tree_removal_test() {
  local case_dir="$TMP_ROOT/refuse-protected"
  local fake_path="$case_dir/fake-bin"
  local install_dir="$case_dir/install"
  local systemctl_log="$case_dir/systemctl.log"
  local protected_etc="$case_dir/protected/etc"
  local status
  make_fake_admin_path "$fake_path"
  make_install_tree "$install_dir"
  mkdir -p "$protected_etc"

  set +e
  PATH="$fake_path:$PATH" \
    SYSTEMCTL_LOG="$systemctl_log" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="/etc" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$install_dir/storage" \
    REMOVE_CONFIG=1 \
    "$REPO_ROOT/scripts/uninstall-systemd.sh" > "$case_dir/uninstall.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "uninstaller accepted protected CONFIG_DIR=/etc"
  assert_file_contains "$case_dir/uninstall.log" "protected system directory"

  set +e
  PATH="$fake_path:$PATH" \
    SYSTEMCTL_LOG="$systemctl_log" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$protected_etc" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="/srv" \
    REMOVE_DATA=1 \
    CONFIRM_REMOVE_DATA="/srv" \
    "$REPO_ROOT/scripts/uninstall-systemd.sh" > "$case_dir/uninstall-data.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "uninstaller accepted protected STORAGE_ROOT=/srv"
  assert_file_contains "$case_dir/uninstall-data.log" "protected system directory"
}

run_remove_config_and_data_test() {
  local case_dir="$TMP_ROOT/remove-all"
  local fake_path="$case_dir/fake-bin"
  local install_dir="$case_dir/install"
  local systemctl_log="$case_dir/systemctl.log"
  make_fake_admin_path "$fake_path"
  make_install_tree "$install_dir"

  PATH="$fake_path:$PATH" \
    SYSTEMCTL_LOG="$systemctl_log" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$install_dir/storage" \
    REMOVE_CONFIG=1 \
    REMOVE_DATA=1 \
    CONFIRM_REMOVE_DATA="$install_dir/storage" \
    "$REPO_ROOT/scripts/uninstall-systemd.sh" > "$case_dir/uninstall.log"

  assert_not_exists "$install_dir/etc/mnemonas"
  assert_not_exists "$install_dir/storage"
  assert_file_contains "$case_dir/uninstall.log" "uninstalled systemd services"
}

run_preserve_data_test
run_refuse_unconfirmed_data_removal_test
run_refuse_protected_tree_removal_test
run_remove_config_and_data_test

printf '[systemd-uninstall-test] all checks passed\n'
