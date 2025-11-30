#!/usr/bin/env bash
# shellcheck disable=SC2016

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fail() {
  printf '[systemd-install-test] ERROR: %s\n' "$*" >&2
  exit 1
}

write_executable() {
  local path="$1"
  shift
  printf '%s\n' "$@" > "$path"
  chmod +x "$path"
}

make_doctor_path_without_ss() {
  local fake_path="$1"
  local target_path="$2"
  local cmd resolved

  mkdir -p "$target_path"
  for cmd in bash awk cat dirname grep mktemp python3 rm sed stat tail; do
    resolved="$(command -v "$cmd")" || fail "required test command is missing: $cmd"
    ln -sf "$resolved" "$target_path/$cmd"
  done
  for cmd in curl df findmnt getent id openssl systemctl timeout ufw; do
    cp "$fake_path/$cmd" "$target_path/$cmd"
  done
}

make_doctor_path_without_python() {
  local fake_path="$1"
  local target_path="$2"
  local cmd resolved

  mkdir -p "$target_path"
  for cmd in bash awk cat dirname grep mktemp rm sed stat tail; do
    resolved="$(command -v "$cmd")" || fail "required test command is missing: $cmd"
    ln -sf "$resolved" "$target_path/$cmd"
  done
  for cmd in curl df findmnt getent id openssl ss systemctl timeout ufw; do
    cp "$fake_path/$cmd" "$target_path/$cmd"
  done
}

make_doctor_path_without_curl() {
  local fake_path="$1"
  local target_path="$2"
  local cmd resolved

  mkdir -p "$target_path"
  for cmd in bash awk cat dirname grep mktemp python3 rm sed stat tail; do
    resolved="$(command -v "$cmd")" || fail "required test command is missing: $cmd"
    ln -sf "$resolved" "$target_path/$cmd"
  done
  for cmd in df findmnt getent id openssl ss systemctl timeout ufw; do
    cp "$fake_path/$cmd" "$target_path/$cmd"
  done
}

make_doctor_path_without_openssl() {
  local fake_path="$1"
  local target_path="$2"
  local cmd resolved

  mkdir -p "$target_path"
  for cmd in bash awk cat dirname grep mktemp python3 rm sed stat tail; do
    resolved="$(command -v "$cmd")" || fail "required test command is missing: $cmd"
    ln -sf "$resolved" "$target_path/$cmd"
  done
  for cmd in curl df findmnt getent id ss systemctl timeout ufw; do
    cp "$fake_path/$cmd" "$target_path/$cmd"
  done
}

make_doctor_path_without_getent() {
  local fake_path="$1"
  local target_path="$2"
  local cmd resolved

  mkdir -p "$target_path"
  for cmd in bash awk cat dirname grep mktemp python3 rm sed stat tail; do
    resolved="$(command -v "$cmd")" || fail "required test command is missing: $cmd"
    ln -sf "$resolved" "$target_path/$cmd"
  done
  for cmd in curl df findmnt id openssl ss systemctl timeout ufw; do
    cp "$fake_path/$cmd" "$target_path/$cmd"
  done
}

make_fake_admin_path() {
  local dir="$1"
  mkdir -p "$dir"
  write_executable "$dir/id" \
    '#!/usr/bin/env bash' \
    'if [[ "$1" == "-u" && "$#" -eq 1 ]]; then printf "0\n"; exit 0; fi' \
    'if [[ "$1" == "-u" ]]; then exit 1; fi' \
    'exit 0'
  write_executable "$dir/getent" '#!/usr/bin/env bash' 'exit 1'
  write_executable "$dir/groupadd" '#!/usr/bin/env bash' 'exit 0'
  write_executable "$dir/useradd" '#!/usr/bin/env bash' 'exit 0'
  write_executable "$dir/chown" '#!/usr/bin/env bash' 'exit 0'
  write_executable "$dir/systemctl" '#!/usr/bin/env bash' 'exit 0'
}

make_release_tree() {
  local dir="$1"
  mkdir -p "$dir/web" "$dir/scripts"
  write_executable "$dir/nasd" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "--check-config" ]]; then exit 0; fi' \
    'exit 0'
  write_executable "$dir/dataplane" '#!/usr/bin/env bash' 'exit 0'
  mkdir -p "$dir/web/assets"
  printf '<div id="root"></div>\n' > "$dir/web/index.html"
  printf 'console.log("mnemonas")\n' > "$dir/web/assets/index.js"
  cp "$REPO_ROOT/mnemonas.example.toml" "$dir/mnemonas.example.toml"
  cp "$REPO_ROOT/scripts/mnemonas-dataplane-start.sh" "$dir/scripts/mnemonas-dataplane-start.sh"
  cp "$REPO_ROOT/scripts/mnemonas-doctor.sh" "$dir/scripts/mnemonas-doctor.sh"
  cp "$REPO_ROOT/scripts/setup-reverse-proxy.sh" "$dir/scripts/setup-reverse-proxy.sh"
  cp "$REPO_ROOT/scripts/uninstall-systemd.sh" "$dir/scripts/uninstall-systemd.sh"
}

assert_file_contains() {
  local path="$1"
  local expected="$2"
  grep -Fq -- "$expected" "$path" || fail "$path does not contain: $expected"
}

assert_file_not_contains() {
  local path="$1"
  local unexpected="$2"
  if grep -Fq -- "$unexpected" "$path"; then
    fail "$path unexpectedly contains: $unexpected"
  fi
}

assert_mode() {
  local path="$1"
  local expected="$2"
  local actual
  actual="$(stat -c '%a' "$path")"
  [[ "$actual" == "$expected" ]] || fail "$path mode is $actual, want $expected"
}

run_fresh_install_test() {
  local case_dir="$TMP_ROOT/fresh"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local storage_dir="$install_dir/storage&root"
  local quoted_initial_password_file
  mkdir -p "$install_dir"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"
  quoted_initial_password_file="$(printf '%q' "$storage_dir/.mnemonas/initial-password.txt")"

  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$storage_dir" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log"

  test -x "$install_dir/bin/nasd" || fail "nasd was not installed"
  test -x "$install_dir/bin/dataplane" || fail "dataplane was not installed"
  test -x "$install_dir/bin/mnemonas-dataplane-start" || fail "dataplane start helper was not installed"
  test -x "$install_dir/bin/mnemonas-doctor" || fail "doctor was not installed"
  test -x "$install_dir/bin/mnemonas-public-setup" || fail "public setup helper was not installed"
  test -x "$install_dir/bin/mnemonas-uninstall-systemd" || fail "uninstaller was not installed"
  test -f "$install_dir/share/mnemonas/web/index.html" || fail "web assets were not installed"
  assert_file_contains "$install_dir/etc/mnemonas/config.toml" "root = \"$storage_dir\""
  assert_file_contains "$install_dir/systemd/mnemonas-dataplane.service" "Environment=CONFIG_PATH=$install_dir/etc/mnemonas/config.toml"
  assert_file_contains "$install_dir/systemd/mnemonas-dataplane.service" "Environment=DATAPLANE_DATA_DIR=$storage_dir/.mnemonas/objects"
  assert_file_contains "$install_dir/systemd/mnemonas-dataplane.service" "ExecStart=$install_dir/bin/mnemonas-dataplane-start"
  assert_file_contains "$install_dir/systemd/mnemonas-dataplane.service" "RequiresMountsFor=$storage_dir"
  assert_file_contains "$install_dir/systemd/mnemonas-dataplane.service" "CapabilityBoundingSet="
  assert_file_contains "$install_dir/systemd/mnemonas-dataplane.service" "RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6"
  assert_file_contains "$install_dir/systemd/mnemonas.service" "MNEMONAS_WEB_DIR=$install_dir/share/mnemonas/web"
  assert_file_contains "$install_dir/systemd/mnemonas.service" "Environment=DATAPLANE_HTTP_ADDR=127.0.0.1:9091"
  assert_file_contains "$install_dir/systemd/mnemonas.service" "RequiresMountsFor=$storage_dir"
  assert_file_contains "$install_dir/systemd/mnemonas.service" "CapabilityBoundingSet="
  assert_file_contains "$install_dir/systemd/mnemonas.service" "RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6"
  assert_file_contains "$case_dir/install.log" "Next steps:"
  assert_file_contains "$case_dir/install.log" "Read initial password: sudo cat $quoted_initial_password_file"
  assert_file_contains "$case_dir/install.log" "Configure public HTTPS: sudo $install_dir/bin/mnemonas-public-setup --proxy caddy <domain> <email>"
  assert_file_contains "$case_dir/install.log" "Keep this release directory; rerun its installer to return to this version after a failed upgrade"
  assert_file_contains "$case_dir/install.log" "Uninstall: sudo $install_dir/bin/mnemonas-uninstall-systemd"
  assert_mode "$storage_dir" "750"
  assert_mode "$storage_dir/files" "750"
  assert_mode "$storage_dir/.mnemonas" "700"
  assert_mode "$storage_dir/.mnemonas/objects" "700"
}

run_storage_ownership_repair_is_explicit_test() {
  local case_dir="$TMP_ROOT/storage-ownership"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local storage_dir="$install_dir/storage"
  local chown_log="$case_dir/chown.log"
  mkdir -p "$storage_dir/files/projects" "$storage_dir/.mnemonas/objects"
  printf 'existing user data\n' > "$storage_dir/files/projects/report.txt"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"
  write_executable "$fake_path/chown" \
    '#!/usr/bin/env bash' \
    'printf "%s\n" "$*" >> "$CHOWN_LOG"' \
    'exit 0'

  CHOWN_LOG="$chown_log" \
    PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$storage_dir" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log"

  assert_file_contains "$storage_dir/files/projects/report.txt" "existing user data"
  assert_file_contains "$case_dir/install.log" "leaving existing storage contents ownership unchanged; set FIX_STORAGE_OWNERSHIP=1 to repair recursively"
  if grep -Fq -- "-R" "$chown_log"; then
    fail "default install recursively changed storage ownership"
  fi

  : > "$chown_log"
  CHOWN_LOG="$chown_log" \
    PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$storage_dir" \
    ENABLE_NOW=0 \
    FIX_STORAGE_OWNERSHIP=1 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/repair.log"

  assert_file_contains "$case_dir/repair.log" "recursively fixing storage ownership under $storage_dir"
  assert_file_contains "$chown_log" "-R mnemonas:mnemonas $storage_dir"
}

run_web_install_preserves_share_sibling_permissions_test() {
  local case_dir="$TMP_ROOT/web-sibling-permissions"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local storage_dir="$install_dir/storage"
  local private_dir="$install_dir/share/mnemonas/private"
  local private_file="$private_dir/secret.txt"
  mkdir -p "$private_dir"
  printf 'secret\n' > "$private_file"
  chmod 0700 "$private_dir"
  chmod 0600 "$private_file"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"

  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    WEB_DIR="$install_dir/share/mnemonas/web" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$storage_dir" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log"

  test -f "$install_dir/share/mnemonas/web/index.html" || fail "web assets were not installed"
  assert_mode "$private_dir" "700"
  assert_mode "$private_file" "600"
}

run_web_install_preserves_existing_assets_on_copy_failure_test() {
  local case_dir="$TMP_ROOT/web-copy-failure"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local storage_dir="$install_dir/storage"
  local web_dir="$install_dir/share/mnemonas/web"
  mkdir -p "$web_dir"
  printf 'old web\n' > "$web_dir/index.html"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"
  write_executable "$fake_path/cp" \
    '#!/usr/bin/env bash' \
    'printf "simulated copy failure\n" >&2' \
    'exit 42'

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    WEB_DIR="$web_dir" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$storage_dir" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "installer succeeded after Web UI copy failure"
  assert_file_contains "$web_dir/index.html" "old web"
  [[ ! -f "$web_dir/assets/index.js" ]] || fail "partial new Web assets were installed after copy failure"
}

run_install_preserves_existing_runtime_on_config_check_failure_test() {
  local case_dir="$TMP_ROOT/config-check-failure"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local storage_dir="$install_dir/storage"
  local web_dir="$install_dir/share/mnemonas/web"
  mkdir -p "$install_dir/bin" "$web_dir"
  write_executable "$install_dir/bin/nasd" '#!/usr/bin/env bash' 'printf "old nasd\n"'
  write_executable "$install_dir/bin/dataplane" '#!/usr/bin/env bash' 'printf "old dataplane\n"'
  write_executable "$install_dir/bin/mnemonas-dataplane-start" '#!/usr/bin/env bash' 'printf "old helper\n"'
  printf 'old web\n' > "$web_dir/index.html"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"
  write_executable "$release_dir/nasd" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "--check-config" ]]; then printf "bad config\n" >&2; exit 64; fi' \
    'printf "new nasd\n"'
  write_executable "$release_dir/dataplane" '#!/usr/bin/env bash' 'printf "new dataplane\n"'
  printf 'new web\n' > "$release_dir/web/index.html"

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    WEB_DIR="$web_dir" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$storage_dir" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "installer succeeded after nasd --check-config failed"
  assert_file_contains "$case_dir/install.log" "bad config"
  assert_file_contains "$install_dir/bin/nasd" "old nasd"
  assert_file_contains "$install_dir/bin/dataplane" "old dataplane"
  assert_file_contains "$install_dir/bin/mnemonas-dataplane-start" "old helper"
  assert_file_contains "$web_dir/index.html" "old web"
  [[ ! -f "$web_dir/assets/index.js" ]] || fail "new Web assets were installed after config check failure"
}

run_install_removes_new_config_on_config_check_failure_test() {
  local case_dir="$TMP_ROOT/new-config-check-failure"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local storage_dir="$install_dir/storage"
  local config_path="$install_dir/etc/mnemonas/config.toml"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"
  write_executable "$release_dir/nasd" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "--check-config" ]]; then printf "bad generated config\n" >&2; exit 64; fi' \
    'printf "new nasd\n"'

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$config_path" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$storage_dir" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "installer succeeded after generated config check failed"
  assert_file_contains "$case_dir/install.log" "bad generated config"
  [[ ! -f "$config_path" ]] || fail "installer left a generated config after config check failure"
  [[ ! -d "$install_dir/bin" ]] || fail "installer created BIN_DIR after config check failure"
  [[ ! -d "$install_dir/share/mnemonas/web" ]] || fail "installer installed Web UI after config check failure"
}

run_install_preserves_existing_runtime_on_binary_install_failure_test() {
  local case_dir="$TMP_ROOT/binary-install-failure"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local storage_dir="$install_dir/storage"
  local web_dir="$install_dir/share/mnemonas/web"
  local real_install
  real_install="$(command -v install)"
  mkdir -p "$install_dir/bin" "$web_dir"
  write_executable "$install_dir/bin/nasd" '#!/usr/bin/env bash' 'printf "old nasd\n"'
  write_executable "$install_dir/bin/dataplane" '#!/usr/bin/env bash' 'printf "old dataplane\n"'
  printf 'old web\n' > "$web_dir/index.html"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"
  write_executable "$release_dir/nasd" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "--check-config" ]]; then exit 0; fi' \
    'printf "new nasd\n"'
  write_executable "$release_dir/dataplane" '#!/usr/bin/env bash' 'printf "new dataplane\n"'
  write_executable "$fake_path/install" \
    '#!/usr/bin/env bash' \
    'dest="${@: -1}"' \
    'if [[ "$dest" == */dataplane ]]; then printf "simulated dataplane install failure\n" >&2; exit 42; fi' \
    "exec \"$real_install\" \"\$@\""

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    WEB_DIR="$web_dir" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$storage_dir" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "installer succeeded after dataplane install failure"
  assert_file_contains "$case_dir/install.log" "simulated dataplane install failure"
  assert_file_contains "$install_dir/bin/nasd" "old nasd"
  assert_file_contains "$install_dir/bin/dataplane" "old dataplane"
  assert_file_contains "$web_dir/index.html" "old web"
}

run_install_rolls_back_late_binary_move_failure_test() {
  local case_dir="$TMP_ROOT/late-binary-move-failure"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local storage_dir="$install_dir/storage"
  local web_dir="$install_dir/share/mnemonas/web"
  local real_mv
  real_mv="$(command -v mv)"
  mkdir -p "$install_dir/bin" "$web_dir"
  write_executable "$install_dir/bin/nasd" '#!/usr/bin/env bash' 'printf "old nasd\n"'
  write_executable "$install_dir/bin/dataplane" '#!/usr/bin/env bash' 'printf "old dataplane\n"'
  write_executable "$install_dir/bin/mnemonas-dataplane-start" '#!/usr/bin/env bash' 'printf "old helper\n"'
  write_executable "$install_dir/bin/mnemonas-doctor" '#!/usr/bin/env bash' 'printf "old doctor\n"'
  write_executable "$install_dir/bin/mnemonas-public-setup" '#!/usr/bin/env bash' 'printf "old public setup\n"'
  write_executable "$install_dir/bin/mnemonas-uninstall-systemd" '#!/usr/bin/env bash' 'printf "old uninstaller\n"'
  printf 'old web\n' > "$web_dir/index.html"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"
  write_executable "$release_dir/nasd" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "--check-config" ]]; then exit 0; fi' \
    'printf "new nasd\n"'
  write_executable "$release_dir/dataplane" '#!/usr/bin/env bash' 'printf "new dataplane\n"'
  write_executable "$release_dir/scripts/mnemonas-dataplane-start.sh" '#!/usr/bin/env bash' 'printf "new helper\n"'
  write_executable "$release_dir/scripts/mnemonas-doctor.sh" '#!/usr/bin/env bash' 'printf "new doctor\n"'
  write_executable "$release_dir/scripts/setup-reverse-proxy.sh" '#!/usr/bin/env bash' 'printf "new public setup\n"'
  write_executable "$release_dir/scripts/uninstall-systemd.sh" '#!/usr/bin/env bash' 'printf "new uninstaller\n"'
  write_executable "$fake_path/mv" \
    '#!/usr/bin/env bash' \
    'args=("$@")' \
    'if [[ "${args[0]:-}" == "--" ]]; then args=("${args[@]:1}"); fi' \
    'src="${args[0]:-}"' \
    'dest="${args[1]:-}"' \
    'if [[ "$src" == */.mnemonas-bin.new.*/* && "$dest" == */mnemonas-doctor ]]; then' \
    '  printf "simulated late doctor install failure\n" >&2' \
    '  exit 42' \
    'fi' \
    "exec \"$real_mv\" \"\$@\""

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    WEB_DIR="$web_dir" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$storage_dir" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "installer succeeded after a late binary move failure"
  assert_file_contains "$case_dir/install.log" "simulated late doctor install failure"
  assert_file_contains "$install_dir/bin/nasd" "old nasd"
  assert_file_contains "$install_dir/bin/dataplane" "old dataplane"
  assert_file_contains "$install_dir/bin/mnemonas-dataplane-start" "old helper"
  assert_file_contains "$install_dir/bin/mnemonas-doctor" "old doctor"
  assert_file_contains "$install_dir/bin/mnemonas-public-setup" "old public setup"
  assert_file_contains "$install_dir/bin/mnemonas-uninstall-systemd" "old uninstaller"
  assert_file_contains "$web_dir/index.html" "old web"
}

run_install_reports_service_restart_failure_test() {
  local case_dir="$TMP_ROOT/restart-failure"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local storage_dir="$install_dir/storage"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"
  write_executable "$fake_path/systemctl" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "restart" && "${2:-}" == "mnemonas.service" ]]; then printf "simulated restart failure\n" >&2; exit 7; fi' \
    'exit 0'

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$storage_dir" \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "installer succeeded after mnemonas restart failed"
  assert_file_contains "$case_dir/install.log" "simulated restart failure"
  assert_file_contains "$case_dir/install.log" "failed to restart mnemonas.service"
  assert_file_contains "$case_dir/install.log" "systemctl status mnemonas.service --no-pager"
  assert_file_contains "$case_dir/install.log" "journalctl -u mnemonas.service -n 100 --no-pager"
  [[ ! -f "$case_dir/install.log" ]] || ! grep -Fq -- "installed successfully" "$case_dir/install.log" || fail "installer reported success after restart failure"
}

run_install_reports_daemon_reload_failure_test() {
  local case_dir="$TMP_ROOT/daemon-reload-failure"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local storage_dir="$install_dir/storage"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"
  write_executable "$fake_path/systemctl" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "daemon-reload" ]]; then printf "simulated daemon reload failure\n" >&2; exit 9; fi' \
    'exit 0'

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$storage_dir" \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "installer succeeded after systemd daemon-reload failed"
  assert_file_contains "$case_dir/install.log" "simulated daemon reload failure"
  assert_file_contains "$case_dir/install.log" "failed to reload systemd units"
  assert_file_contains "$case_dir/install.log" "systemctl cat mnemonas.service mnemonas-dataplane.service"
  assert_file_contains "$case_dir/install.log" "systemctl status mnemonas.service --no-pager"
  [[ ! -f "$case_dir/install.log" ]] || ! grep -Fq -- "installed successfully" "$case_dir/install.log" || fail "installer reported success after daemon-reload failure"
}

run_install_reports_service_enable_failure_test() {
  local case_dir="$TMP_ROOT/enable-failure"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local storage_dir="$install_dir/storage"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"
  write_executable "$fake_path/systemctl" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "enable" ]]; then printf "simulated enable failure\n" >&2; exit 8; fi' \
    'exit 0'

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$storage_dir" \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "installer succeeded after systemd enable failed"
  assert_file_contains "$case_dir/install.log" "simulated enable failure"
  assert_file_contains "$case_dir/install.log" "failed to enable systemd units"
  assert_file_contains "$case_dir/install.log" "systemctl status mnemonas.service --no-pager"
  assert_file_contains "$case_dir/install.log" "systemctl status mnemonas-dataplane.service --no-pager"
  assert_file_contains "$case_dir/install.log" "journalctl -u mnemonas.service -u mnemonas-dataplane.service -n 100 --no-pager"
  [[ ! -f "$case_dir/install.log" ]] || ! grep -Fq -- "installed successfully" "$case_dir/install.log" || fail "installer reported success after enable failure"
}

run_successful_upgrade_preserves_config_and_data_test() {
  local case_dir="$TMP_ROOT/upgrade-success"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local storage_dir="$case_dir/upgrade-storage"
  local config_path="$install_dir/etc/mnemonas/config.toml"
  local expected_config="$case_dir/expected-config.toml"
  local web_dir="$install_dir/share/mnemonas/web"
  mkdir -p "$install_dir/bin" "$install_dir/etc/mnemonas" "$web_dir/assets" "$storage_dir/files/projects" "$storage_dir/.mnemonas"
  write_executable "$install_dir/bin/nasd" '#!/usr/bin/env bash' 'printf "old nasd\n"'
  write_executable "$install_dir/bin/dataplane" '#!/usr/bin/env bash' 'printf "old dataplane\n"'
  write_executable "$install_dir/bin/mnemonas-dataplane-start" '#!/usr/bin/env bash' 'printf "old helper\n"'
  write_executable "$install_dir/bin/mnemonas-doctor" '#!/usr/bin/env bash' 'printf "old doctor\n"'
  write_executable "$install_dir/bin/mnemonas-public-setup" '#!/usr/bin/env bash' 'printf "old public setup\n"'
  write_executable "$install_dir/bin/mnemonas-uninstall-systemd" '#!/usr/bin/env bash' 'printf "old uninstaller\n"'
  printf 'old web\n' > "$web_dir/index.html"
  printf 'old-only asset\n' > "$web_dir/assets/legacy.js"
  printf 'existing user data\n' > "$storage_dir/files/projects/report.txt"
  printf '{"users":[]}\n' > "$storage_dir/.mnemonas/users.json"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"
  write_executable "$release_dir/nasd" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "--check-config" ]]; then exit 0; fi' \
    'printf "new nasd\n"'
  write_executable "$release_dir/dataplane" '#!/usr/bin/env bash' 'printf "new dataplane\n"'
  write_executable "$release_dir/scripts/mnemonas-dataplane-start.sh" '#!/usr/bin/env bash' 'printf "new helper\n"'
  write_executable "$release_dir/scripts/mnemonas-doctor.sh" '#!/usr/bin/env bash' 'printf "new doctor\n"'
  write_executable "$release_dir/scripts/setup-reverse-proxy.sh" '#!/usr/bin/env bash' 'printf "new public setup\n"'
  write_executable "$release_dir/scripts/uninstall-systemd.sh" '#!/usr/bin/env bash' 'printf "new uninstaller\n"'
  printf 'new web\n' > "$release_dir/web/index.html"
  printf 'new asset\n' > "$release_dir/web/assets/index.js"

  cat > "$expected_config" <<EOF
[server]
host = "127.0.0.1"
port = 18181

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"
EOF
  cp "$expected_config" "$config_path"

  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    WEB_DIR="$web_dir" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$config_path" \
    SYSTEMD_DIR="$install_dir/systemd" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log"

  cmp -s "$expected_config" "$config_path" || fail "installer rewrote existing config.toml during upgrade"
  assert_file_contains "$case_dir/install.log" "keeping existing config: $config_path"
  assert_file_contains "$case_dir/install.log" "Open Web UI: http://127.0.0.1:18181"
  assert_file_contains "$case_dir/install.log" "installed successfully"
  assert_file_contains "$storage_dir/files/projects/report.txt" "existing user data"
  assert_file_contains "$storage_dir/.mnemonas/users.json" '"users":[]'
  assert_file_contains "$install_dir/bin/nasd" "new nasd"
  assert_file_contains "$install_dir/bin/dataplane" "new dataplane"
  assert_file_contains "$install_dir/bin/mnemonas-dataplane-start" "new helper"
  assert_file_contains "$install_dir/bin/mnemonas-doctor" "new doctor"
  assert_file_contains "$install_dir/bin/mnemonas-public-setup" "new public setup"
  assert_file_contains "$install_dir/bin/mnemonas-uninstall-systemd" "new uninstaller"
  assert_file_contains "$web_dir/index.html" "new web"
  assert_file_contains "$web_dir/assets/index.js" "new asset"
  [[ ! -f "$web_dir/assets/legacy.js" ]] || fail "upgrade left stale Web UI assets behind"
  assert_file_contains "$install_dir/systemd/mnemonas-dataplane.service" "Environment=DATAPLANE_GRPC_ADDR=127.0.0.1:19090"
  assert_file_contains "$install_dir/systemd/mnemonas-dataplane.service" "Environment=DATAPLANE_DATA_DIR=$storage_dir/.mnemonas/objects"
  assert_file_contains "$install_dir/systemd/mnemonas.service" "Environment=MNEMONAS_WEB_DIR=$web_dir"
  assert_file_contains "$install_dir/systemd/mnemonas.service" "ReadWritePaths=$storage_dir $install_dir/etc/mnemonas"
}

run_systemd_release_rollback_preserves_config_and_data_test() {
  local case_dir="$TMP_ROOT/release-rollback"
  local fake_path="$case_dir/fake-bin"
  local previous_release_dir="$case_dir/release-previous"
  local upgraded_release_dir="$case_dir/release-upgraded"
  local install_dir="$case_dir/install"
  local storage_dir="$case_dir/rollback-storage"
  local config_path="$install_dir/etc/mnemonas/config.toml"
  local expected_config="$case_dir/expected-config.toml"
  local web_dir="$install_dir/share/mnemonas/web"
  mkdir -p "$install_dir/etc/mnemonas" "$storage_dir/files/projects" "$storage_dir/.mnemonas"
  make_fake_admin_path "$fake_path"
  make_release_tree "$previous_release_dir"
  make_release_tree "$upgraded_release_dir"

  write_executable "$previous_release_dir/nasd" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "--check-config" ]]; then exit 0; fi' \
    'printf "previous nasd\n"'
  write_executable "$previous_release_dir/dataplane" '#!/usr/bin/env bash' 'printf "previous dataplane\n"'
  write_executable "$previous_release_dir/scripts/mnemonas-dataplane-start.sh" '#!/usr/bin/env bash' 'printf "previous helper\n"'
  write_executable "$previous_release_dir/scripts/mnemonas-doctor.sh" '#!/usr/bin/env bash' 'printf "previous doctor\n"'
  write_executable "$previous_release_dir/scripts/setup-reverse-proxy.sh" '#!/usr/bin/env bash' 'printf "previous public setup\n"'
  write_executable "$previous_release_dir/scripts/uninstall-systemd.sh" '#!/usr/bin/env bash' 'printf "previous uninstaller\n"'
  printf 'previous web\n' > "$previous_release_dir/web/index.html"
  printf 'previous asset\n' > "$previous_release_dir/web/assets/previous.js"

  write_executable "$upgraded_release_dir/nasd" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "--check-config" ]]; then exit 0; fi' \
    'printf "upgraded nasd\n"'
  write_executable "$upgraded_release_dir/dataplane" '#!/usr/bin/env bash' 'printf "upgraded dataplane\n"'
  write_executable "$upgraded_release_dir/scripts/mnemonas-dataplane-start.sh" '#!/usr/bin/env bash' 'printf "upgraded helper\n"'
  write_executable "$upgraded_release_dir/scripts/mnemonas-doctor.sh" '#!/usr/bin/env bash' 'printf "upgraded doctor\n"'
  write_executable "$upgraded_release_dir/scripts/setup-reverse-proxy.sh" '#!/usr/bin/env bash' 'printf "upgraded public setup\n"'
  write_executable "$upgraded_release_dir/scripts/uninstall-systemd.sh" '#!/usr/bin/env bash' 'printf "upgraded uninstaller\n"'
  printf 'upgraded web\n' > "$upgraded_release_dir/web/index.html"
  printf 'upgraded asset\n' > "$upgraded_release_dir/web/assets/upgraded.js"

  cat > "$expected_config" <<EOF
[server]
host = "127.0.0.1"
port = 18182

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19190"
EOF
  cp "$expected_config" "$config_path"
  printf 'existing rollback user data\n' > "$storage_dir/files/projects/report.txt"
  printf '{"users":[{"username":"admin"}]}\n' > "$storage_dir/.mnemonas/users.json"

  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$upgraded_release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    WEB_DIR="$web_dir" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$config_path" \
    SYSTEMD_DIR="$install_dir/systemd" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/upgrade.log"

  assert_file_contains "$install_dir/bin/nasd" "upgraded nasd"
  assert_file_contains "$install_dir/bin/dataplane" "upgraded dataplane"
  assert_file_contains "$install_dir/bin/mnemonas-dataplane-start" "upgraded helper"
  assert_file_contains "$install_dir/bin/mnemonas-doctor" "upgraded doctor"
  assert_file_contains "$install_dir/bin/mnemonas-public-setup" "upgraded public setup"
  assert_file_contains "$install_dir/bin/mnemonas-uninstall-systemd" "upgraded uninstaller"
  assert_file_contains "$web_dir/index.html" "upgraded web"
  assert_file_contains "$web_dir/assets/upgraded.js" "upgraded asset"
  cmp -s "$expected_config" "$config_path" || fail "upgrade rewrote existing config.toml before rollback"

  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$previous_release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    WEB_DIR="$web_dir" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$config_path" \
    SYSTEMD_DIR="$install_dir/systemd" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/rollback.log"

  cmp -s "$expected_config" "$config_path" || fail "rollback rewrote existing config.toml"
  assert_file_contains "$case_dir/rollback.log" "keeping existing config: $config_path"
  assert_file_contains "$case_dir/rollback.log" "Open Web UI: http://127.0.0.1:18182"
  assert_file_contains "$case_dir/rollback.log" "installed successfully"
  assert_file_contains "$storage_dir/files/projects/report.txt" "existing rollback user data"
  assert_file_contains "$storage_dir/.mnemonas/users.json" '"username":"admin"'
  assert_file_contains "$install_dir/bin/nasd" "previous nasd"
  assert_file_contains "$install_dir/bin/dataplane" "previous dataplane"
  assert_file_contains "$install_dir/bin/mnemonas-dataplane-start" "previous helper"
  assert_file_contains "$install_dir/bin/mnemonas-doctor" "previous doctor"
  assert_file_contains "$install_dir/bin/mnemonas-public-setup" "previous public setup"
  assert_file_contains "$install_dir/bin/mnemonas-uninstall-systemd" "previous uninstaller"
  assert_file_contains "$web_dir/index.html" "previous web"
  assert_file_contains "$web_dir/assets/previous.js" "previous asset"
  [[ ! -f "$web_dir/assets/upgraded.js" ]] || fail "rollback left upgraded-only Web UI assets behind"
  assert_file_contains "$install_dir/systemd/mnemonas-dataplane.service" "Environment=DATAPLANE_GRPC_ADDR=127.0.0.1:19190"
  assert_file_contains "$install_dir/systemd/mnemonas-dataplane.service" "Environment=DATAPLANE_DATA_DIR=$storage_dir/.mnemonas/objects"
  assert_file_contains "$install_dir/systemd/mnemonas.service" "Environment=MNEMONAS_WEB_DIR=$web_dir"
  assert_file_contains "$install_dir/systemd/mnemonas.service" "ReadWritePaths=$storage_dir $install_dir/etc/mnemonas"
}

run_source_checkout_stale_binary_test() {
  local case_dir="$TMP_ROOT/stale-source-binary"
  local fake_path="$case_dir/fake-bin"
  local checkout_dir="$case_dir/checkout"
  local install_dir="$case_dir/install"

  make_fake_admin_path "$fake_path"
  mkdir -p \
    "$checkout_dir/.git" \
    "$checkout_dir/bin" \
    "$checkout_dir/cmd/nasd" \
    "$checkout_dir/internal/auth" \
    "$checkout_dir/dataplane/src" \
    "$checkout_dir/proto" \
    "$checkout_dir/scripts" \
    "$checkout_dir/web/dist/assets" \
    "$checkout_dir/web/src"

  write_executable "$checkout_dir/bin/nasd" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "--check-config" ]]; then exit 0; fi' \
    'exit 0'
  write_executable "$checkout_dir/bin/dataplane" '#!/usr/bin/env bash' 'exit 0'
  cp "$REPO_ROOT/scripts/install-systemd.sh" "$checkout_dir/scripts/install-systemd.sh"
  cp "$REPO_ROOT/scripts/mnemonas-dataplane-start.sh" "$checkout_dir/scripts/mnemonas-dataplane-start.sh"
  cp "$REPO_ROOT/scripts/mnemonas-doctor.sh" "$checkout_dir/scripts/mnemonas-doctor.sh"
  cp "$REPO_ROOT/scripts/setup-reverse-proxy.sh" "$checkout_dir/scripts/setup-reverse-proxy.sh"
  cp "$REPO_ROOT/scripts/uninstall-systemd.sh" "$checkout_dir/scripts/uninstall-systemd.sh"
  cp "$REPO_ROOT/mnemonas.example.toml" "$checkout_dir/mnemonas.example.toml"
  printf '<div id="root"></div>\n' > "$checkout_dir/web/dist/index.html"
  printf 'console.log("mnemonas")\n' > "$checkout_dir/web/dist/assets/index.js"
  printf 'package github.com/seanbao/mnemonas/cmd/nasd\n' > "$checkout_dir/cmd/nasd/main.go"
  printf 'package auth\n' > "$checkout_dir/internal/auth/handler.go"
  printf 'fn main() {}\n' > "$checkout_dir/dataplane/src/main.rs"
  printf 'syntax = "proto3";\n' > "$checkout_dir/proto/dataplane.proto"
  touch -d '2026-01-01 00:00:00Z' "$checkout_dir/bin/nasd" "$checkout_dir/bin/dataplane" "$checkout_dir/web/dist/index.html"
  touch -d '2026-01-02 00:00:00Z' "$checkout_dir/internal/auth/handler.go"

  set +e
  (
    cd "$checkout_dir"
    PATH="$fake_path:$PATH" \
      BIN_DIR="$install_dir/bin" \
      SHARE_DIR="$install_dir/share/mnemonas" \
      CONFIG_DIR="$install_dir/etc/mnemonas" \
      CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
      SYSTEMD_DIR="$install_dir/systemd" \
      STORAGE_ROOT="$install_dir/storage" \
      ENABLE_NOW=0 \
      "$checkout_dir/scripts/install-systemd.sh"
  ) > "$case_dir/install.log" 2>&1
  local status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "installer accepted a stale nasd binary from a source checkout"
  assert_file_contains "$case_dir/install.log" "nasd binary is older than Go sources"
  [[ ! -d "$install_dir/bin" ]] || fail "installer created files after rejecting stale source artifacts"

  install_dir="$case_dir/explicit-install"
  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$checkout_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$install_dir/storage" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/explicit-install.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "installer accepted a stale nasd binary from an explicit source checkout"
  assert_file_contains "$case_dir/explicit-install.log" "nasd binary is older than Go sources"
  [[ ! -d "$install_dir/bin" ]] || fail "installer created files after rejecting explicit stale source artifacts"
}

run_existing_config_test() {
  local case_dir="$TMP_ROOT/existing"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local storage_dir="$case_dir/custom#storage"
  local config_path="$install_dir/etc/mnemonas/config.toml"
  local expected_config="$case_dir/expected-config.toml"
  local quoted_initial_password_file
  mkdir -p "$install_dir/etc/mnemonas" "$storage_dir"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"
  quoted_initial_password_file="$(printf '%q' "$storage_dir/custom-auth/initial-password.txt")"

  cat > "$expected_config" <<EOF
[server]
host = "127.0.0.1"
port = 18080

[ storage ] # storage root may have comments in hand-edited TOML
root = "$case_dir/custom\u0023storage" # TOML escapes may encode characters in quoted values

[ dataplane ] # dataplane endpoint
grpc_address = "127.0.0.1\u003a19090"

[ dataplane . cdc ] # chunking profile
min_chunk_size = 524288
avg_chunk_size = 2097152
max_chunk_size = 8388608

[auth]
users_file = "~/custom-auth/users.json"
EOF
  cp "$expected_config" "$config_path"

  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$config_path" \
    SYSTEMD_DIR="$install_dir/systemd" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log"

  cmp -s "$expected_config" "$config_path" || fail "installer rewrote existing config.toml during upgrade"
  assert_file_contains "$case_dir/install.log" "Open Web UI: http://127.0.0.1:18080"
  assert_file_contains "$case_dir/install.log" "Read initial password: sudo cat $quoted_initial_password_file"
  assert_file_contains "$install_dir/systemd/mnemonas-dataplane.service" "Environment=DATAPLANE_GRPC_ADDR=127.0.0.1:19090"
  assert_file_contains "$install_dir/systemd/mnemonas-dataplane.service" "Environment=DATAPLANE_DATA_DIR=$storage_dir/.mnemonas/objects"
  assert_file_contains "$install_dir/systemd/mnemonas.service" "Environment=DATAPLANE_HTTP_ADDR=127.0.0.1:9091"
  assert_file_contains "$install_dir/systemd/mnemonas.service" "ReadWritePaths=$storage_dir $install_dir/etc/mnemonas"

  write_executable "$case_dir/capture-dataplane" \
    '#!/usr/bin/env bash' \
    'printf "%s\n" "$*" > "$CAPTURE_FILE"'
  CAPTURE_FILE="$case_dir/dataplane.args" \
    CONFIG_PATH="$config_path" \
    DATAPLANE_BIN="$case_dir/capture-dataplane" \
    DATAPLANE_DATA_DIR="$storage_dir/.mnemonas/objects" \
    "$install_dir/bin/mnemonas-dataplane-start"
  assert_file_contains "$case_dir/dataplane.args" "--grpc 127.0.0.1:19090"
  assert_file_contains "$case_dir/dataplane.args" "--data-dir $storage_dir/.mnemonas/objects"
  assert_file_contains "$case_dir/dataplane.args" "--min-chunk-size 524288"
  assert_file_contains "$case_dir/dataplane.args" "--avg-chunk-size 2097152"
  assert_file_contains "$case_dir/dataplane.args" "--max-chunk-size 8388608"
}

run_invalid_input_test() {
  local case_dir="$TMP_ROOT/invalid-input"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local status
  mkdir -p "$install_dir"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$install_dir/storage" \
    SERVER_PORT="8080 bad" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "invalid SERVER_PORT was accepted"
  assert_file_contains "$case_dir/install.log" "SERVER_PORT cannot contain whitespace"
  [[ ! -f "$install_dir/systemd/mnemonas.service" ]] || fail "systemd unit was written after invalid input"
}

run_service_account_validation_test() {
  local case_dir="$TMP_ROOT/service-account-validation"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local status
  mkdir -p "$install_dir"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$install_dir/storage" \
    SERVICE_USER=root \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "installer accepted SERVICE_USER=root"
  assert_file_contains "$case_dir/install.log" "SERVICE_USER must not be root"
  [[ ! -d "$install_dir/bin" ]] || fail "installer created files after rejecting SERVICE_USER=root"
}

run_server_host_validation_test() {
  local case_dir="$TMP_ROOT/server-host-validation"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local invalid_install_dir="$case_dir/invalid-install"
  local ipv6_install_dir="$case_dir/ipv6-install"
  local status
  mkdir -p "$invalid_install_dir" "$ipv6_install_dir"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$invalid_install_dir/bin" \
    SHARE_DIR="$invalid_install_dir/share/mnemonas" \
    CONFIG_DIR="$invalid_install_dir/etc/mnemonas" \
    CONFIG_PATH="$invalid_install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$invalid_install_dir/systemd" \
    STORAGE_ROOT="$invalid_install_dir/storage" \
    SERVER_HOST="[::1]:8080" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/invalid.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "SERVER_HOST with port was accepted"
  assert_file_contains "$case_dir/invalid.log" "SERVER_HOST must not include brackets"
  [[ ! -d "$invalid_install_dir/bin" ]] || fail "installer created files after rejecting invalid SERVER_HOST"

  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$ipv6_install_dir/bin" \
    SHARE_DIR="$ipv6_install_dir/share/mnemonas" \
    CONFIG_DIR="$ipv6_install_dir/etc/mnemonas" \
    CONFIG_PATH="$ipv6_install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$ipv6_install_dir/systemd" \
    STORAGE_ROOT="$ipv6_install_dir/storage" \
    SERVER_HOST="::1" \
    SERVER_PORT="18080" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/ipv6.log"

  assert_file_contains "$case_dir/ipv6.log" "Open Web UI: http://[::1]:18080"
}

run_server_port_normalization_test() {
  local case_dir="$TMP_ROOT/server-port-normalization"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  mkdir -p "$install_dir"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"

  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$install_dir/storage" \
    SERVER_HOST="127.0.0.1" \
    SERVER_PORT="018080" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log"

  assert_file_contains "$case_dir/install.log" "Open Web UI: http://127.0.0.1:18080"
  assert_file_contains "$install_dir/etc/mnemonas/config.toml" "port = 18080"
}

run_protected_web_dir_test() {
  local case_dir="$TMP_ROOT/protected-web-dir"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local status
  mkdir -p "$install_dir"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    WEB_DIR="/usr/local" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$install_dir/storage" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "protected WEB_DIR was accepted"
  assert_file_contains "$case_dir/install.log" "WEB_DIR points at a protected system directory"
  [[ ! -d "$install_dir/bin" ]] || fail "installer created files after rejecting protected WEB_DIR"
}

run_share_dir_overlap_test() {
  local case_dir="$TMP_ROOT/share-dir-overlap"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local status
  mkdir -p "$install_dir"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/storage" \
    WEB_DIR="$install_dir/share/mnemonas/web" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$install_dir/storage" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "SHARE_DIR overlapping STORAGE_ROOT was accepted"
  assert_file_contains "$case_dir/install.log" "SHARE_DIR must not overlap STORAGE_ROOT"
  [[ ! -d "$install_dir/bin" ]] || fail "installer created files after rejecting overlapping SHARE_DIR"
}

run_web_dir_overlap_test() {
  local case_dir="$TMP_ROOT/web-dir-overlap"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local status
  mkdir -p "$install_dir"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    WEB_DIR="$install_dir/storage/web" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$install_dir/storage" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "WEB_DIR under STORAGE_ROOT was accepted"
  assert_file_contains "$case_dir/install.log" "WEB_DIR must not overlap STORAGE_ROOT"
  [[ ! -d "$install_dir/bin" ]] || fail "installer created files after rejecting overlapping WEB_DIR"
}

run_core_path_overlap_test() {
  local case_dir="$TMP_ROOT/core-path-overlap"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local status
  mkdir -p "$install_dir"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/storage/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$install_dir/storage" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/bin-storage.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "BIN_DIR under STORAGE_ROOT was accepted"
  assert_file_contains "$case_dir/bin-storage.log" "BIN_DIR must not overlap STORAGE_ROOT"
  [[ ! -d "$install_dir/storage/bin" ]] || fail "installer created BIN_DIR after rejecting overlap with STORAGE_ROOT"

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/storage/.mnemonas" \
    CONFIG_PATH="$install_dir/storage/.mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$install_dir/storage" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/config-storage.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "CONFIG_DIR under STORAGE_ROOT was accepted"
  assert_file_contains "$case_dir/config-storage.log" "CONFIG_DIR must not overlap STORAGE_ROOT"
  [[ ! -d "$install_dir/bin" ]] || fail "installer created files after rejecting CONFIG_DIR overlap"
}

run_protected_config_and_storage_test() {
  local case_dir="$TMP_ROOT/protected-config-storage"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local status
  mkdir -p "$install_dir"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="/etc" \
    CONFIG_PATH="/etc/mnemonas.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$install_dir/storage" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/config.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "protected CONFIG_DIR was accepted"
  assert_file_contains "$case_dir/config.log" "CONFIG_DIR points at a protected system directory"
  [[ ! -d "$install_dir/bin" ]] || fail "installer created files after rejecting protected CONFIG_DIR"

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="/srv" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/storage.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "protected STORAGE_ROOT was accepted"
  assert_file_contains "$case_dir/storage.log" "STORAGE_ROOT points at a protected system directory"
  [[ ! -d "$install_dir/bin" ]] || fail "installer created files after rejecting protected STORAGE_ROOT"
}

run_config_path_scope_test() {
  local case_dir="$TMP_ROOT/config-path-scope"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local status
  mkdir -p "$install_dir"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/outside.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$install_dir/storage" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/outside.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "CONFIG_PATH outside CONFIG_DIR was accepted"
  assert_file_contains "$case_dir/outside.log" "CONFIG_PATH must be inside CONFIG_DIR"
  [[ ! -d "$install_dir/bin" ]] || fail "installer created files after rejecting out-of-scope CONFIG_PATH"

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/../outside.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$install_dir/storage" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/parent.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "CONFIG_PATH with parent directory segment was accepted"
  assert_file_contains "$case_dir/parent.log" "CONFIG_PATH cannot contain parent directory segments"
}

run_systemd_specifier_rejection_test() {
  local case_dir="$TMP_ROOT/systemd-specifier"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local status
  mkdir -p "$install_dir"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$install_dir/storage-%h" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "systemd specifier in STORAGE_ROOT was accepted"
  assert_file_contains "$case_dir/install.log" "STORAGE_ROOT cannot contain systemd specifiers"
  [[ ! -d "$install_dir/bin" ]] || fail "installer created files after rejecting systemd specifier"
}

run_symlink_path_rejection_test() {
  local case_dir="$TMP_ROOT/symlink-path"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local target_dir="$case_dir/target"
  local link_dir="$case_dir/link"
  local status
  mkdir -p "$install_dir" "$target_dir"
  ln -s "$target_dir" "$link_dir"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$link_dir/storage" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "symlinked STORAGE_ROOT parent was accepted"
  assert_file_contains "$case_dir/install.log" "STORAGE_ROOT must not contain symlink path components"
  [[ ! -d "$target_dir/storage" ]] || fail "installer created storage through a symlink parent"
  [[ ! -d "$install_dir/bin" ]] || fail "installer created files after rejecting symlinked path"
}

run_storage_subdir_symlink_rejection_test() {
  local case_dir="$TMP_ROOT/storage-subdir-symlink"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local storage_dir="$install_dir/storage"
  local outside_internal="$case_dir/outside-internal"
  local outside_files="$case_dir/outside-files"
  local status
  mkdir -p "$storage_dir" "$outside_internal" "$outside_files"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"

  ln -s "$outside_internal" "$storage_dir/.mnemonas"
  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$storage_dir" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/internal.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "installer accepted a symlink internal metadata root"
  assert_file_contains "$case_dir/internal.log" "storage internal object directory must not contain symlink path components"
  [[ ! -d "$outside_internal/objects" ]] || fail "installer created objects through a symlink internal metadata root"

  rm -f "$storage_dir/.mnemonas"
  ln -s "$outside_files" "$storage_dir/files"
  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$storage_dir" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/files.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "installer accepted a symlink files directory"
  assert_file_contains "$case_dir/files.log" "storage files directory must not contain symlink path components"
}

run_systemd_newline_rejection_test() {
  local case_dir="$TMP_ROOT/systemd-newline"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local grpc_addr
  local status
  mkdir -p "$install_dir"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"

  grpc_addr="127.0.0.1:19090"$'\n'"Environment=INJECTED=1"
  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$install_dir/storage" \
    DATAPLANE_GRPC_ADDR="$grpc_addr" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "newline in DATAPLANE_GRPC_ADDR was accepted"
  assert_file_contains "$case_dir/install.log" "DATAPLANE_GRPC_ADDR cannot contain newline characters"
  [[ ! -d "$install_dir/bin" ]] || fail "installer created files after rejecting systemd newline"
}

run_systemd_control_character_rejection_test() {
  local case_dir="$TMP_ROOT/systemd-control"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local storage_root
  local status
  mkdir -p "$install_dir"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"

  storage_root="$install_dir/storage"$'\a'"root"
  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$storage_root" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "control character in STORAGE_ROOT was accepted"
  assert_file_contains "$case_dir/install.log" "STORAGE_ROOT cannot contain control characters"
  [[ ! -d "$install_dir/bin" ]] || fail "installer created files after rejecting control-character path"
}

run_dataplane_addr_validation_test() {
  local case_dir="$TMP_ROOT/dataplane-addr-validation"
  local fake_path="$case_dir/fake-bin"
  local release_dir="$case_dir/release"
  local install_dir="$case_dir/install"
  local status
  mkdir -p "$install_dir"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"

  set +e
  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    STORAGE_ROOT="$install_dir/storage" \
    DATAPLANE_HTTP_ADDR="127.0.0.1:70000" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "invalid DATAPLANE_HTTP_ADDR port was accepted"
  assert_file_contains "$case_dir/install.log" "DATAPLANE_HTTP_ADDR port must be between 1 and 65535"
  [[ ! -d "$install_dir/bin" ]] || fail "installer created files after rejecting invalid dataplane address"
}

run_doctor_config_test() {
  local case_dir="$TMP_ROOT/doctor"
  local fake_path="$case_dir/fake-bin"
  local bin_dir="$case_dir/bin"
  local web_dir="$case_dir/web"
  local storage_dir="$case_dir/storage-root"
  local backup_dir="$case_dir/backup"
  local systemd_dir="$case_dir/systemd"
  local fake_admin_hash='$2a$10$ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0'
  mkdir -p "$fake_path" "$bin_dir" "$web_dir" "$storage_dir/.mnemonas" "$backup_dir" "$systemd_dir"
  chmod 0750 "$storage_dir"
  chmod 0700 "$storage_dir/.mnemonas"
  cat > "$storage_dir/.mnemonas/users.json" <<EOF
[
  {"id":"admin-1","username":"admin","password_hash":"$fake_admin_hash","role":"admin","disabled":false}
]
EOF
  chmod 0600 "$storage_dir/.mnemonas/users.json"
  cat > "$storage_dir/secrets.json" <<'JSON'
{"jwt_secret":"test-jwt-secret-value","webdav_password":"GeneratedPass123"}
JSON
  chmod 0600 "$storage_dir/secrets.json"

  write_executable "$bin_dir/nasd" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "--check-config" ]]; then exit 0; fi' \
    'exit 0'
  write_executable "$bin_dir/dataplane" '#!/usr/bin/env bash' 'exit 0'
  printf '<div id="root"></div>\n' > "$web_dir/index.html"

  write_executable "$fake_path/id" '#!/usr/bin/env bash' 'exit 0'
  write_executable "$fake_path/getent" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "ahosts" && "${2:-}" == "nas.example.com" ]]; then printf "203.0.113.10 STREAM nas.example.com\n"; exit 0; fi' \
    'exit 1'
  write_executable "$fake_path/runuser" '#!/usr/bin/env bash' 'shift 3; "$@"'
  write_executable "$fake_path/systemctl" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "is-active" ]]; then exit 0; fi' \
    'exit 0'
  write_executable "$fake_path/curl" \
    '#!/usr/bin/env bash' \
    'last="${@: -1}"' \
    'case "$last" in */) printf "<div id=\"root\"></div>\n";; *) printf "ok\n";; esac'
  write_executable "$fake_path/ss" \
    '#!/usr/bin/env bash' \
    'printf "LISTEN 0 4096 127.0.0.1:18080 0.0.0.0:*\n"' \
    'printf "LISTEN 0 4096 127.0.0.1:19090 0.0.0.0:*\n"' \
    'printf "LISTEN 0 4096 127.0.0.1:19091 0.0.0.0:*\n"'
  write_executable "$fake_path/findmnt" \
    '#!/usr/bin/env bash' \
    'path="${@: -1}"' \
    'case "$path" in' \
    "  $backup_dir|$case_dir/non-writable-backup|$case_dir/symlink-backup)" \
    '    printf "tank/backup zfs %s\n" "$path"' \
    '    ;;' \
    '  *)' \
    '    printf "tank/data zfs %s\n" "$path"' \
    '    ;;' \
    'esac'
  write_executable "$fake_path/df" \
    '#!/usr/bin/env bash' \
    'printf "Filesystem 1024-blocks Used Available Capacity Mounted on\n"' \
    'printf "/dev/fake 20971520 5242880 15728640 25%% %s\n" "${@: -1}"'
  write_executable "$fake_path/tailscale" '#!/usr/bin/env bash' 'exit 0'
  write_executable "$fake_path/ufw" \
    '#!/usr/bin/env bash' \
    'printf "Status: active\n\n"' \
    'printf "To                         Action      From\n"' \
    'printf "18080/tcp                  ALLOW       192.168.0.0/16\n"'

  cat > "$case_dir/config.toml" <<EOF
[server]
host = "0.0.0.0"
port = 18080

[storage]
# TOML basic strings may encode characters that are valid in paths.
root = "$case_dir/storage\u002droot"

[dataplane]
grpc_address = "127.0.0.1:19090"
EOF
  chmod 0600 "$case_dir/config.toml"

  cat > "$systemd_dir/mnemonas-dataplane.service" <<EOF
[Service]
Environment="DATAPLANE_HTTP_ADDR=127.0.0.1:19091" "OTHER=value"
EOF

  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    SYSTEMD_DIR="$systemd_dir" \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/doctor.log"

  assert_file_contains "$case_dir/doctor.log" "Storage: $storage_dir"
  assert_file_contains "$case_dir/doctor.log" "control plane port 18080 is listening"
  assert_file_contains "$case_dir/doctor.log" "dataplane gRPC port 19090 is listening"
  assert_file_contains "$case_dir/doctor.log" "dataplane gRPC port 19090 is loopback-only"
  assert_file_contains "$case_dir/doctor.log" "dataplane HTTP port 19091 is loopback-only"
  assert_file_contains "$case_dir/doctor.log" "config file is private to its owner: $case_dir/config.toml"
  assert_file_contains "$case_dir/doctor.log" "storage disk space: 15.0 GiB available / 20.0 GiB total (25% used)"
  assert_file_contains "$case_dir/doctor.log" "users file directory is private to its owner: $storage_dir/.mnemonas"
  assert_file_contains "$case_dir/doctor.log" "users file is private to its owner: $storage_dir/.mnemonas/users.json"
  assert_file_contains "$case_dir/doctor.log" "generated secrets file is private to its owner: $storage_dir/secrets.json"
  assert_file_contains "$case_dir/doctor.log" "Web UI/API authentication is enabled"
  assert_file_contains "$case_dir/doctor.log" "WebDAV authentication is configured: basic"
  assert_file_contains "$case_dir/doctor.log" "administrator availability verified: 1 enabled administrator(s)"
  assert_file_contains "$case_dir/doctor.log" "backup root is outside storage root: $backup_dir"
  assert_file_contains "$case_dir/doctor.log" "backup root is writable by current user: $backup_dir"
  assert_file_contains "$case_dir/doctor.log" "backup root is on a separate filesystem source: tank/backup"
  assert_file_contains "$case_dir/doctor.log" "ufw is active"
  assert_file_contains "$case_dir/doctor.log" "Summary: 0 failure(s)"

  cat > "$storage_dir/.mnemonas/users.json" <<'JSON'
[]
JSON
  chmod 0600 "$storage_dir/.mnemonas/users.json"
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    SYSTEMD_DIR="$systemd_dir" \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/doctor-no-admin.log"

  assert_file_contains "$case_dir/doctor-no-admin.log" "users file has no enabled administrators; MnemoNAS will create a recovery administrator on next startup if auth is enabled"
  assert_file_contains "$case_dir/doctor-no-admin.log" "Summary: 0 failure(s), 1 warning(s)"
  cat > "$storage_dir/.mnemonas/users.json" <<EOF
[
  {"id":"admin-1","username":"admin","password_hash":"$fake_admin_hash","role":"admin","disabled":false}
]
EOF
  chmod 0600 "$storage_dir/.mnemonas/users.json"

  cat > "$case_dir/config-unsafe-no-auth.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
enabled = false

[webdav]
auth_type = "none"

[security]
allow_unsafe_no_auth = true
EOF
  chmod 0600 "$case_dir/config-unsafe-no-auth.toml"

  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-unsafe-no-auth.toml" \
    SYSTEMD_DIR="$systemd_dir" \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/doctor-unsafe-no-auth.log"

  assert_file_contains "$case_dir/doctor-unsafe-no-auth.log" "auth.enabled=false; Web UI/API access relies on a controlled network, VPN, or outer access-control layer"
  assert_file_contains "$case_dir/doctor-unsafe-no-auth.log" "WebDAV auth_type=none; restrict access with loopback binding, VPN, firewall, or another trusted boundary"
  assert_file_contains "$case_dir/doctor-unsafe-no-auth.log" "security.allow_unsafe_no_auth=true; verify that an outer boundary deliberately restricts unauthenticated access"
  assert_file_contains "$case_dir/doctor-unsafe-no-auth.log" "Summary: 0 failure(s), 3 warning(s)"

  chmod 0644 "$storage_dir/.mnemonas/users.json" "$storage_dir/secrets.json"
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    SYSTEMD_DIR="$systemd_dir" \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/doctor-open-sensitive-files.log"
  chmod 0600 "$storage_dir/.mnemonas/users.json" "$storage_dir/secrets.json"

  assert_file_contains "$case_dir/doctor-open-sensitive-files.log" "users file is not private"
  assert_file_contains "$case_dir/doctor-open-sensitive-files.log" "generated secrets file is not private"
  assert_file_contains "$case_dir/doctor-open-sensitive-files.log" "Summary: 0 failure(s), 2 warning(s)"

  local real_runtime_auth_parent="$case_dir/real-runtime-auth"
  local linked_runtime_auth_parent="$case_dir/linked-runtime-auth"
  mkdir -p "$real_runtime_auth_parent/private"
  cp "$storage_dir/.mnemonas/users.json" "$real_runtime_auth_parent/private/users.json"
  chmod 0700 "$real_runtime_auth_parent" "$real_runtime_auth_parent/private"
  chmod 0600 "$real_runtime_auth_parent/private/users.json"
  ln -s "$real_runtime_auth_parent" "$linked_runtime_auth_parent"
  cat > "$case_dir/config-runtime-symlink-component.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
users_file = "$linked_runtime_auth_parent/private/users.json"
EOF
  chmod 0600 "$case_dir/config-runtime-symlink-component.toml"

  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-runtime-symlink-component.toml" \
    SYSTEMD_DIR="$systemd_dir" \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/doctor-runtime-symlink-component.log"

  assert_file_contains "$case_dir/doctor-runtime-symlink-component.log" "users file directory path contains a symlink component; use a regular private directory path: $linked_runtime_auth_parent"
  assert_file_contains "$case_dir/doctor-runtime-symlink-component.log" "users file path contains a symlink component; use a regular private file path: $linked_runtime_auth_parent"
  assert_file_contains "$case_dir/doctor-runtime-symlink-component.log" "initial admin password path contains a symlink component at $linked_runtime_auth_parent"
  assert_file_contains "$case_dir/doctor-runtime-symlink-component.log" "Summary: 0 failure(s), 3 warning(s)"

  chmod 0644 "$case_dir/config.toml"
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    SYSTEMD_DIR="$systemd_dir" \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/doctor-open-config.log"
  chmod 0600 "$case_dir/config.toml"

  assert_file_contains "$case_dir/doctor-open-config.log" "config file is not private"
  assert_file_contains "$case_dir/doctor-open-config.log" "Summary: 0 failure(s), 1 warning(s)"

  local non_writable_backup_dir="$case_dir/non-writable-backup"
  mkdir -p "$non_writable_backup_dir"
  chmod 0550 "$non_writable_backup_dir"
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    SYSTEMD_DIR="$systemd_dir" \
    BACKUP_ROOT="$non_writable_backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/doctor-backup-not-writable.log"
  chmod 0750 "$non_writable_backup_dir"

  assert_file_contains "$case_dir/doctor-backup-not-writable.log" "backup root is not writable by current user and runuser is unavailable: $non_writable_backup_dir"
  assert_file_contains "$case_dir/doctor-backup-not-writable.log" "Summary: 0 failure(s), 1 warning(s)"

  local same_source_backup_dir="$case_dir/same-source-backup"
  mkdir -p "$same_source_backup_dir"
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    SYSTEMD_DIR="$systemd_dir" \
    BACKUP_ROOT="$same_source_backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/doctor-backup-same-source.log"

  assert_file_contains "$case_dir/doctor-backup-same-source.log" "backup root shares filesystem source with storage root (tank/data)"
  assert_file_contains "$case_dir/doctor-backup-same-source.log" "Summary: 0 failure(s), 1 warning(s)"

  local symlink_backup_dir="$case_dir/symlink-backup"
  ln -s "$backup_dir" "$symlink_backup_dir"
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    SYSTEMD_DIR="$systemd_dir" \
    BACKUP_ROOT="$symlink_backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/doctor-backup-symlink.log"

  assert_file_contains "$case_dir/doctor-backup-symlink.log" "backup root path is a symlink; use a real directory, dataset, mount point, or remote target: $symlink_backup_dir"
  assert_file_contains "$case_dir/doctor-backup-symlink.log" "backup root exists: $symlink_backup_dir"
  assert_file_contains "$case_dir/doctor-backup-symlink.log" "Summary: 0 failure(s), 1 warning(s)"

  local backup_file="$case_dir/backup-file"
  printf 'not a directory\n' > "$backup_file"
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    SYSTEMD_DIR="$systemd_dir" \
    BACKUP_ROOT="$backup_file" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/doctor-backup-file.log"

  assert_file_contains "$case_dir/doctor-backup-file.log" "backup root is not a directory: $backup_file"
  assert_file_contains "$case_dir/doctor-backup-file.log" "Summary: 0 failure(s), 1 warning(s)"

  write_executable "$fake_path/ss" \
    '#!/usr/bin/env bash' \
    'printf "LISTEN 0 4096 127.0.0.1:18080 0.0.0.0:*\n"' \
    'printf "LISTEN 0 4096 0.0.0.0:19090 0.0.0.0:*\n"' \
    'printf "LISTEN 0 4096 [::]:19091 [::]:*\n"'
  write_executable "$fake_path/ufw" \
    '#!/usr/bin/env bash' \
    'printf "Status: active\n\n"' \
    'printf "To                         Action      From\n"' \
    'printf "19090/tcp                  ALLOW       Anywhere\n"' \
    'printf "19091/tcp                  ALLOW       Anywhere\n"'

  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    SYSTEMD_DIR="$systemd_dir" \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/doctor-unsafe.log"

  assert_file_contains "$case_dir/doctor-unsafe.log" "dataplane gRPC port 19090 is listening beyond loopback (0.0.0.0:19090)"
  assert_file_contains "$case_dir/doctor-unsafe.log" "dataplane HTTP port 19091 is listening beyond loopback ([::]:19091)"
  assert_file_contains "$case_dir/doctor-unsafe.log" "ufw appears to allow dataplane gRPC port 19090"
  assert_file_contains "$case_dir/doctor-unsafe.log" "ufw appears to allow dataplane HTTP port 19091"
  assert_file_contains "$case_dir/doctor-unsafe.log" "Summary: 0 failure(s), 4 warning(s)"

  cat > "$case_dir/proc-net-tcp" <<'EOF'
  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:46A0 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1
   1: 00000000:4A92 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2
EOF
  cat > "$case_dir/proc-net-tcp6" <<'EOF'
  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:4A93 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 3
EOF
  write_executable "$fake_path/ufw" \
    '#!/usr/bin/env bash' \
    'printf "Status: active\n\n"' \
    'printf "To                         Action      From\n"'

  PATH="$fake_path:$PATH" \
    MNEMONAS_DOCTOR_DISABLE_SS=1 \
    MNEMONAS_PROC_NET_TCP_PATH="$case_dir/proc-net-tcp" \
    MNEMONAS_PROC_NET_TCP6_PATH="$case_dir/proc-net-tcp6" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    SYSTEMD_DIR="$systemd_dir" \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/doctor-proc-net.log"

  assert_file_contains "$case_dir/doctor-proc-net.log" "control plane port 18080 is listening"
  assert_file_contains "$case_dir/doctor-proc-net.log" "dataplane gRPC port 19090 is listening beyond loopback (0.0.0.0:19090)"
  assert_file_contains "$case_dir/doctor-proc-net.log" "dataplane HTTP port 19091 is listening beyond loopback ([::]:19091)"

  mkdir -p "$storage_dir/backups"
  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    SYSTEMD_DIR="$systemd_dir" \
    BACKUP_ROOT="$storage_dir/backups" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/doctor-backup-inside-storage.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "doctor accepted a backup root inside storage root"
  assert_file_contains "$case_dir/doctor-backup-inside-storage.log" "backup root must not be inside storage root: $storage_dir/backups"
  assert_file_contains "$case_dir/doctor-backup-inside-storage.log" "Use a separate disk, dataset, or remote target."

  chmod 0644 "$bin_dir/dataplane"
  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    SYSTEMD_DIR="$systemd_dir" \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/doctor-non-executable.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "doctor accepted a non-executable dataplane binary"
  assert_file_contains "$case_dir/doctor-non-executable.log" "dataplane binary is not executable: $bin_dir/dataplane"
}

run_doctor_public_domain_test() {
  local case_dir="$TMP_ROOT/doctor-public"
  local fake_path="$case_dir/fake-bin"
  local bin_dir="$case_dir/bin"
  local web_dir="$case_dir/web"
  local storage_dir="$case_dir/storage"
  local backup_dir="$case_dir/backup"
  local fake_admin_hash='$2a$10$ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0'
  local status
  mkdir -p "$fake_path" "$bin_dir" "$web_dir" "$storage_dir/files" "$storage_dir/.mnemonas" "$backup_dir"
  chmod 0750 "$storage_dir" "$storage_dir/files"
  chmod 0700 "$storage_dir/.mnemonas"

  write_executable "$bin_dir/nasd" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "--check-config" ]]; then exit 0; fi' \
    'exit 0'
  write_executable "$bin_dir/dataplane" '#!/usr/bin/env bash' 'exit 0'
  printf '<div id="root"></div>\n' > "$web_dir/index.html"
  cat > "$storage_dir/.mnemonas/users.json" <<EOF
[
  {"id":"admin-1","username":"admin","password_hash":"$fake_admin_hash","role":"admin","disabled":false},
  {"id":"admin-2","username":"backup-admin","password_hash":"$fake_admin_hash","role":"admin","disabled":false},
  {"id":"disabled-admin","username":"disabled-admin","role":"admin","disabled":true}
]
EOF
  chmod 0600 "$storage_dir/.mnemonas/users.json"
  cat > "$storage_dir/secrets.json" <<'EOF'
{"jwt_secret":"test-jwt-secret-value-with-enough-length","webdav_password":"GeneratedPass123"}
EOF
  chmod 0600 "$storage_dir/secrets.json"

  write_executable "$fake_path/id" '#!/usr/bin/env bash' 'exit 0'
  write_executable "$fake_path/getent" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "ahosts" && "${2:-}" == "nas.example.com" ]]; then printf "203.0.113.10 STREAM nas.example.com\n"; exit 0; fi' \
    'exit 1'
  write_executable "$fake_path/systemctl" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "is-active" ]]; then exit 0; fi' \
    'exit 0'
  write_executable "$fake_path/curl" \
    '#!/usr/bin/env bash' \
    'url="${@: -1}"' \
    'headers_file=""' \
    'previous=""' \
    'for arg in "$@"; do' \
    '  if [[ "$previous" == "-D" ]]; then headers_file="$arg"; fi' \
    '  previous="$arg"' \
    'done' \
    'write_share_probe_headers() {' \
    '  [[ -n "$headers_file" && "$headers_file" != "-" ]] || return 0' \
    '  printf "HTTP/2 404\r\nCache-Control: private, no-cache\r\nVary: Cookie\r\nX-Content-Type-Options: nosniff\r\nReferrer-Policy: no-referrer\r\n\r\n" > "$headers_file"' \
    '}' \
    'if [[ "$*" == *" -X PROPFIND "* && "$url" == "https://nas.example.com/dav/" ]]; then printf "401"; exit 0; fi' \
    'if [[ "$url" == "https://nas.example.com/api/v1/public/shares/mnemonas-doctor-probe" ]]; then write_share_probe_headers; printf "404"; exit 0; fi' \
    'if [[ "$*" == *" -w "* && "$url" == "http://nas.example.com/health" ]]; then printf "301 https://nas.example.com/health"; exit 0; fi' \
    'case "$url" in' \
    '  https://nas.example.com/health) printf "ok\n";;' \
    '  http://nas.example.com:18080/health) exit 7;;' \
    '  */) printf "<div id=\"root\"></div>\n";;' \
    '  *) printf "ok\n";;' \
    'esac'
  write_executable "$fake_path/openssl" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "s_client" ]]; then' \
    '  printf "%s\n" "-----BEGIN CERTIFICATE-----" "FAKE" "-----END CERTIFICATE-----" "Verify return code: 0 (ok)"' \
    '  exit 0' \
    'fi' \
    'if [[ "${1:-}" == "x509" ]]; then' \
    '  case " $* " in' \
    '    *" -checkend "*) [[ "${MNEMONAS_FAKE_CERT_EXPIRES_SOON:-0}" == "1" ]] && exit 1; exit 0;;' \
    '    *" -enddate "*) printf "notAfter=Jun 01 12:00:00 2026 GMT\n"; exit 0;;' \
    '  esac' \
    'fi' \
    'exit 1'
  write_executable "$fake_path/timeout" \
    '#!/usr/bin/env bash' \
    'if [[ "${2:-}" == "openssl" ]]; then shift; exec "$@"; fi' \
    'if [[ "${MNEMONAS_FAKE_PUBLIC_CONTROL_TCP_OPEN:-0}" == "1" && "$*" == *"/dev/tcp/nas.example.com/18080"* ]]; then exit 0; fi' \
    'if [[ "${MNEMONAS_FAKE_PUBLIC_DATAPLANE_GRPC_TCP_OPEN:-0}" == "1" && "$*" == *"/dev/tcp/nas.example.com/19090"* ]]; then exit 0; fi' \
    'if [[ "${MNEMONAS_FAKE_PUBLIC_DATAPLANE_HTTP_TCP_OPEN:-0}" == "1" && "$*" == *"/dev/tcp/nas.example.com/19091"* ]]; then exit 0; fi' \
    'exit 1'
  write_executable "$fake_path/ss" \
    '#!/usr/bin/env bash' \
    'printf "LISTEN 0 4096 127.0.0.1:18080 0.0.0.0:*\n"' \
    'printf "LISTEN 0 4096 127.0.0.1:19090 0.0.0.0:*\n"' \
    'printf "LISTEN 0 4096 127.0.0.1:19091 0.0.0.0:*\n"'
  write_executable "$fake_path/findmnt" \
    '#!/usr/bin/env bash' \
    'printf "tank/data zfs %s\n" "${@: -1}"'
  write_executable "$fake_path/df" \
    '#!/usr/bin/env bash' \
    'printf "Filesystem 1024-blocks Used Available Capacity Mounted on\n"' \
    'printf "/dev/fake 20971520 5242880 15728640 25%% %s\n" "${@: -1}"'
  write_executable "$fake_path/ufw" \
    '#!/usr/bin/env bash' \
    'printf "Status: active\n"'

  cat > "$case_dir/config.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:019090"

[webdav]
prefix = " /files/team/../../dav/ "
EOF

  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=019091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public.log"

  assert_file_contains "$case_dir/doctor-public.log" "Public access checks for nas.example.com"
  assert_file_contains "$case_dir/doctor-public.log" "getent is available for public DNS diagnostics"
  assert_file_contains "$case_dir/doctor-public.log" "public domain resolves locally: nas.example.com (203.0.113.10)"
  assert_file_contains "$case_dir/doctor-public.log" "public backend host is loopback-only: 127.0.0.1"
  assert_file_contains "$case_dir/doctor-public.log" "trusted proxy hops configured: 1"
  assert_file_contains "$case_dir/doctor-public.log" "public HTTPS health reachable: https://nas.example.com/health"
  assert_file_contains "$case_dir/doctor-public.log" "public HTTP redirects to HTTPS: http://nas.example.com/health -> https://nas.example.com/health"
  assert_file_contains "$case_dir/doctor-public.log" "public HTTPS certificate matches nas.example.com"
  assert_file_contains "$case_dir/doctor-public.log" "public HTTPS certificate is valid for at least 30 days"
  assert_file_contains "$case_dir/doctor-public.log" "certificate automation detected: Caddy"
  assert_file_contains "$case_dir/doctor-public.log" "public config file path has no symlink components"
  assert_file_contains "$case_dir/doctor-public.log" "public auth.access_token_ttl is within 1h: 15m"
  assert_file_contains "$case_dir/doctor-public.log" "public auth.refresh_token_ttl is within 720h: 168h"
  assert_file_contains "$case_dir/doctor-public.log" "public users file directory is private to its owner: $storage_dir/.mnemonas"
  assert_file_contains "$case_dir/doctor-public.log" "public users file is private to its owner: $storage_dir/.mnemonas/users.json"
  assert_file_contains "$case_dir/doctor-public.log" "public administrator redundancy verified: 2 enabled administrators"
  assert_file_contains "$case_dir/doctor-public.log" "public WebDAV generated password file is private to its owner: $storage_dir/secrets.json"
  assert_file_contains "$case_dir/doctor-public.log" "public WebDAV generated Basic Auth password is available"
  assert_file_contains "$case_dir/doctor-public.log" "public WebDAV anonymous PROPFIND is rejected: https://nas.example.com/dav/ (HTTP 401)"
  assert_file_contains "$case_dir/doctor-public.log" "public direct control plane is not publicly reachable: http://nas.example.com:18080/health"
  assert_file_contains "$case_dir/doctor-public.log" "public direct control plane TCP port 18080 is not publicly reachable on nas.example.com"
  assert_file_contains "$case_dir/doctor-public.log" "public dataplane gRPC port 19090 is not publicly reachable on nas.example.com"
  assert_file_contains "$case_dir/doctor-public.log" "public dataplane HTTP port 19091 is not publicly reachable on nas.example.com"
  assert_file_contains "$case_dir/doctor-public.log" "control plane port 18080 is loopback-only"
  assert_file_contains "$case_dir/doctor-public.log" "dataplane gRPC port 19090 is loopback-only"
  assert_file_contains "$case_dir/doctor-public.log" "dataplane HTTP port 19091 is loopback-only"
  assert_file_contains "$case_dir/doctor-public.log" "manual cloud firewall check: expose only 80/443 publicly; keep 18080/19090/19091 closed to the public internet"
  assert_file_contains "$case_dir/doctor-public.log" "Summary: 0 failure(s)"

  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=019091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain NAS.EXAMPLE.COM. > "$case_dir/doctor-public-normalized-domain.log"

  assert_file_contains "$case_dir/doctor-public-normalized-domain.log" "Public access checks for nas.example.com"
  assert_file_contains "$case_dir/doctor-public-normalized-domain.log" "public domain resolves locally: nas.example.com (203.0.113.10)"
  assert_file_contains "$case_dir/doctor-public-normalized-domain.log" "public HTTPS health reachable: https://nas.example.com/health"
  assert_file_contains "$case_dir/doctor-public-normalized-domain.log" "public HTTP redirects to HTTPS: http://nas.example.com/health -> https://nas.example.com/health"
  assert_file_contains "$case_dir/doctor-public-normalized-domain.log" "Summary: 0 failure(s)"

  cat > "$case_dir/public-proc-net-tcp" <<'EOF'
  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:46A0 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1
   1: 00000000:4A92 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2
EOF
  cat > "$case_dir/public-proc-net-tcp6" <<'EOF'
  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:4A93 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 3
EOF

  local no_ss_path="$case_dir/no-ss-bin"
  make_doctor_path_without_ss "$fake_path" "$no_ss_path"
  set +e
  PATH="$no_ss_path" \
    MNEMONAS_PROC_NET_TCP_PATH="$case_dir/public-proc-net-tcp" \
    MNEMONAS_PROC_NET_TCP6_PATH="$case_dir/public-proc-net-tcp6" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=019091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-proc-net-open.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted non-loopback backend ports when only /proc/net/tcp was available"
  assert_file_contains "$case_dir/doctor-public-proc-net-open.log" "dataplane gRPC port 19090 is listening beyond loopback (0.0.0.0:19090)"
  assert_file_contains "$case_dir/doctor-public-proc-net-open.log" "dataplane HTTP port 19091 is listening beyond loopback ([::]:19091)"

  cat > "$case_dir/public-proc-net-tcp-loopback-only" <<'EOF'
  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:46A0 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1
   1: 0100007F:4A92 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2
   2: 0100007F:4A93 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 3
EOF

  set +e
  PATH="$no_ss_path" \
    MNEMONAS_PROC_NET_TCP_PATH="$case_dir/public-proc-net-tcp-loopback-only" \
    MNEMONAS_PROC_NET_TCP6_PATH="$case_dir/missing-public-proc-net-tcp6" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=019091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-proc-net-missing-ipv6.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted incomplete local port inspection without ss"
  assert_file_contains "$case_dir/doctor-public-proc-net-missing-ipv6.log" "control plane port 18080 cannot be fully inspected"
  assert_file_contains "$case_dir/doctor-public-proc-net-missing-ipv6.log" "$case_dir/public-proc-net-tcp-loopback-only"
  assert_file_contains "$case_dir/doctor-public-proc-net-missing-ipv6.log" "$case_dir/missing-public-proc-net-tcp6"

  write_executable "$fake_path/ufw" \
    '#!/usr/bin/env bash' \
    'printf "Status: active\n\n"' \
    'printf "To                         Action      From\n"' \
    'printf "18080/tcp                  ALLOW       Anywhere\n"'

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=019091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-ufw-backend-open.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted broad UFW allow rule for control plane"
  assert_file_contains "$case_dir/doctor-public-ufw-backend-open.log" "ufw appears to broadly allow public control plane port 18080"

  write_executable "$fake_path/ufw" \
    '#!/usr/bin/env bash' \
    'printf "Status: active\n\n"' \
    'printf "To                         Action      From\n"' \
    'printf "19090/tcp                  ALLOW       Anywhere\n"'

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=019091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-ufw-dataplane-grpc-open.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted broad UFW allow rule for dataplane gRPC"
  assert_file_contains "$case_dir/doctor-public-ufw-dataplane-grpc-open.log" "ufw appears to broadly allow public dataplane gRPC port 19090"

  write_executable "$fake_path/ufw" \
    '#!/usr/bin/env bash' \
    'printf "Status: active\n\n"' \
    'printf "To                         Action      From\n"' \
    'printf "19091/tcp                  ALLOW       Anywhere (v6)\n"'

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=019091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-ufw-dataplane-http-open.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted broad UFW allow rule for dataplane HTTP"
  assert_file_contains "$case_dir/doctor-public-ufw-dataplane-http-open.log" "ufw appears to broadly allow public dataplane HTTP port 19091"

  write_executable "$fake_path/ufw" \
    '#!/usr/bin/env bash' \
    'printf "Status: active\n"'

  local linked_config_file="$case_dir/config-linked.toml"
  ln -s "$case_dir/config.toml" "$linked_config_file"
  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$linked_config_file" \
    DATAPLANE_HTTP_PORT=019091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-symlink-config.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted a symlink config file"
  assert_file_contains "$case_dir/doctor-public-symlink-config.log" "public config file path is a symlink; use a regular private config file: $linked_config_file"

  local real_config_component_parent="$case_dir/real-config-component"
  local linked_config_component_parent="$case_dir/linked-config-component"
  mkdir -p "$real_config_component_parent"
  cp "$case_dir/config.toml" "$real_config_component_parent/config.toml"
  ln -s "$real_config_component_parent" "$linked_config_component_parent"
  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$linked_config_component_parent/config.toml" \
    DATAPLANE_HTTP_PORT=019091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-symlink-config-component.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted a symlink config path component"
  assert_file_contains "$case_dir/doctor-public-symlink-config-component.log" "public config file path contains a symlink component; use a regular private config path: $linked_config_component_parent"

  cat > "$case_dir/config-long-auth-ttl.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
access_token_ttl = "2h"
refresh_token_ttl = "1080h"
EOF

  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-long-auth-ttl.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-long-auth-ttl.log"

  assert_file_contains "$case_dir/doctor-public-long-auth-ttl.log" "public auth.access_token_ttl is longer than 1h: 2h"
  assert_file_contains "$case_dir/doctor-public-long-auth-ttl.log" "public auth.refresh_token_ttl is longer than 720h: 1080h"
  assert_file_contains "$case_dir/doctor-public-long-auth-ttl.log" "Summary: 0 failure(s)"

  local open_auth_dir="$case_dir/open-auth"
  mkdir -p "$open_auth_dir"
  cp "$storage_dir/.mnemonas/users.json" "$open_auth_dir/users.json"
  chmod 0755 "$open_auth_dir"
  chmod 0644 "$open_auth_dir/users.json"
  cat > "$case_dir/config-open-auth-users.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
users_file = "$open_auth_dir/users.json"
EOF

  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-open-auth-users.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-open-auth-users.log"

  assert_file_contains "$case_dir/doctor-public-open-auth-users.log" "public users file directory is not private"
  assert_file_contains "$case_dir/doctor-public-open-auth-users.log" "public users file is not private"
  assert_file_contains "$case_dir/doctor-public-open-auth-users.log" "Summary: 0 failure(s)"

  local home_dir="$case_dir/home"
  local home_storage_dir="$home_dir/storage"
  local home_auth_dir="$home_dir/auth"
  mkdir -p "$home_storage_dir/files" "$home_storage_dir/.mnemonas" "$home_auth_dir"
  chmod 0750 "$home_storage_dir" "$home_storage_dir/files"
  chmod 0700 "$home_storage_dir/.mnemonas" "$home_auth_dir"
  cp "$storage_dir/.mnemonas/users.json" "$home_auth_dir/users.json"
  cp "$storage_dir/secrets.json" "$home_storage_dir/secrets.json"
  chmod 0600 "$home_auth_dir/users.json" "$home_storage_dir/secrets.json"
  touch "$home_auth_dir/initial-password.txt"
  chmod 0600 "$home_auth_dir/initial-password.txt"
  local literal_home_initial_password
  literal_home_initial_password="$(printf '%s' '~')/auth/initial-password.txt"
  cat > "$case_dir/config-home-auth-users.toml" <<'EOF'
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "~/storage"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
users_file = "~/auth/users.json"
EOF

  set +e
  HOME="$home_dir" \
    STORAGE_ROOT='' \
    PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-home-auth-users.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-home-auth-users.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted home-expanded initial password file"
  assert_file_contains "$case_dir/doctor-public-home-auth-users.log" "Storage: $home_storage_dir"
  assert_file_contains "$case_dir/doctor-public-home-auth-users.log" "public users file is private to its owner: $home_auth_dir/users.json"
  assert_file_contains "$case_dir/doctor-public-home-auth-users.log" "initial admin password file still exists at $home_auth_dir/initial-password.txt"
  assert_file_not_contains "$case_dir/doctor-public-home-auth-users.log" "$literal_home_initial_password"

  local linked_users_file="$case_dir/linked-users.json"
  ln -s "$storage_dir/.mnemonas/users.json" "$linked_users_file"
  cat > "$case_dir/config-symlink-users.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
users_file = "$linked_users_file"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-symlink-users.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-symlink-users.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted a symlink users file"
  assert_file_contains "$case_dir/doctor-public-symlink-users.log" "public users file path is a symlink; use a regular private file"

  local real_auth_dir="$case_dir/real-auth"
  local linked_auth_dir="$case_dir/linked-auth"
  mkdir -p "$real_auth_dir"
  cp "$storage_dir/.mnemonas/users.json" "$real_auth_dir/users.json"
  chmod 0700 "$real_auth_dir"
  chmod 0600 "$real_auth_dir/users.json"
  ln -s "$real_auth_dir" "$linked_auth_dir"
  cat > "$case_dir/config-symlink-auth-dir.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
users_file = "$linked_auth_dir/users.json"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-symlink-auth-dir.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-symlink-auth-dir.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted a symlink users file directory"
  assert_file_contains "$case_dir/doctor-public-symlink-auth-dir.log" "public users file directory path is a symlink; use a regular private directory"

  local real_auth_component_parent="$case_dir/real-auth-component"
  local linked_auth_component_parent="$case_dir/linked-auth-component"
  mkdir -p "$real_auth_component_parent/private"
  cp "$storage_dir/.mnemonas/users.json" "$real_auth_component_parent/private/users.json"
  chmod 0700 "$real_auth_component_parent" "$real_auth_component_parent/private"
  chmod 0600 "$real_auth_component_parent/private/users.json"
  ln -s "$real_auth_component_parent" "$linked_auth_component_parent"
  cat > "$case_dir/config-symlink-auth-component.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
users_file = "$linked_auth_component_parent/private/users.json"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-symlink-auth-component.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-symlink-auth-component.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted a symlink users file path component"
  assert_file_contains "$case_dir/doctor-public-symlink-auth-component.log" "public users file directory path contains a symlink component; use a regular private directory path: $linked_auth_component_parent"
  assert_file_contains "$case_dir/doctor-public-symlink-auth-component.log" "initial admin password path contains a symlink component at $linked_auth_component_parent"

  local custom_auth_dir="$case_dir/custom-auth"
  mkdir -p "$custom_auth_dir"
  cp "$storage_dir/.mnemonas/users.json" "$custom_auth_dir/users.json"
  touch "$custom_auth_dir/initial-password.txt"
  cat > "$case_dir/config-custom-auth-initial-password.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
users_file = "$custom_auth_dir/users.json"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-custom-auth-initial-password.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-custom-auth-initial-password.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted custom auth initial password file"
  assert_file_contains "$case_dir/doctor-public-custom-auth-initial-password.log" "initial admin password file still exists"
  assert_file_contains "$case_dir/doctor-public-custom-auth-initial-password.log" "$custom_auth_dir/initial-password.txt"

  local symlink_auth_dir="$case_dir/symlink-auth"
  mkdir -p "$symlink_auth_dir"
  cp "$storage_dir/.mnemonas/users.json" "$symlink_auth_dir/users.json"
  ln -s "$case_dir/missing-initial-password.txt" "$symlink_auth_dir/initial-password.txt"
  cat > "$case_dir/config-symlink-auth-initial-password.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
users_file = "$symlink_auth_dir/users.json"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-symlink-auth-initial-password.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-symlink-auth-initial-password.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted custom auth initial password symlink"
  assert_file_contains "$case_dir/doctor-public-symlink-auth-initial-password.log" "initial admin password path is a symlink"
  assert_file_contains "$case_dir/doctor-public-symlink-auth-initial-password.log" "$symlink_auth_dir/initial-password.txt"

  cat > "$case_dir/config-share-trimmed.toml" <<EOF
[server]
host = "LOCALHOST"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = " https://NAS.EXAMPLE.COM./shares "
default_max_access = 20
EOF

  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-trimmed.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-trimmed.log"

  assert_file_contains "$case_dir/doctor-public-share-trimmed.log" "public backend host is loopback-only: LOCALHOST"
  assert_file_contains "$case_dir/doctor-public-share-trimmed.log" "public share.base_url uses HTTPS on nas.example.com"
  assert_file_contains "$case_dir/doctor-public-share-trimmed.log" "public share.default_expires_in is within 720h: 168h"
  assert_file_contains "$case_dir/doctor-public-share-trimmed.log" "public share.default_max_access limits new share link accesses: 20"
  assert_file_contains "$case_dir/doctor-public-share-trimmed.log" "public share JSON responses use private cache and Cookie Vary boundaries"
  assert_file_contains "$case_dir/doctor-public-share-trimmed.log" "Summary: 0 failure(s)"

  cat > "$case_dir/config-share-route-prefix.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://nas.example.com/s/"
EOF

  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-route-prefix.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-route-prefix.log"

  assert_file_contains "$case_dir/doctor-public-share-route-prefix.log" "public share.base_url should be the site origin or base path before /s; current value will generate nested /s/s share links"
  assert_file_contains "$case_dir/doctor-public-share-route-prefix.log" "Summary: 0 failure(s)"

  cat > "$case_dir/config-share-escaped-route-prefix.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://nas.example.com/base%2Fs"
EOF

  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-escaped-route-prefix.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-escaped-route-prefix.log"

  assert_file_contains "$case_dir/doctor-public-share-escaped-route-prefix.log" "public share.base_url should be the site origin or base path before /s; current value will generate nested /s/s share links"
  assert_file_contains "$case_dir/doctor-public-share-escaped-route-prefix.log" "Summary: 0 failure(s)"

  cat > "$case_dir/config-share-duplicate-slashes.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://nas.example.com/shares//team"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-duplicate-slashes.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-duplicate-slashes.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted share.base_url path with duplicate slashes"
  assert_file_contains "$case_dir/doctor-public-share-duplicate-slashes.log" "public share.base_url path must not contain duplicate slashes"

  cat > "$case_dir/config-share-escaped-duplicate-slashes.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://nas.example.com/shares%2F%2Fteam"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-escaped-duplicate-slashes.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-escaped-duplicate-slashes.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted share.base_url path with escaped duplicate slashes"
  assert_file_contains "$case_dir/doctor-public-share-escaped-duplicate-slashes.log" "public share.base_url path must not contain duplicate slashes"

  cat > "$case_dir/config-share-backslash-path.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = 'https://nas.example.com/shares\team'
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-backslash-path.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-backslash-path.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted share.base_url path with backslashes"
  assert_file_contains "$case_dir/doctor-public-share-backslash-path.log" "public share.base_url path must not contain backslashes"

  cat > "$case_dir/config-share-host-relative-backslash-path.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = 'https://nas.example.com\shares'
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-host-relative-backslash-path.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-host-relative-backslash-path.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted share.base_url host-relative path with backslashes"
  assert_file_contains "$case_dir/doctor-public-share-host-relative-backslash-path.log" "public share.base_url path must not contain backslashes"

  cat > "$case_dir/config-share-escaped-backslash-path.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://nas.example.com/shares%5Cteam"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-escaped-backslash-path.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-escaped-backslash-path.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted share.base_url path with escaped backslashes"
  assert_file_contains "$case_dir/doctor-public-share-escaped-backslash-path.log" "public share.base_url path must not contain backslashes"

  cat > "$case_dir/config-share-no-expiry.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://nas.example.com"
default_expires_in = "0h"
EOF

  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-no-expiry.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-no-expiry.log"

  assert_file_contains "$case_dir/doctor-public-share-no-expiry.log" "public share.default_expires_in leaves new share links without an expiry"
  assert_file_contains "$case_dir/doctor-public-share-no-expiry.log" "Summary: 0 failure(s)"

  cat > "$case_dir/config-share-long-expiry.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://nas.example.com"
default_expires_in = "1080h"
EOF

  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-long-expiry.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-long-expiry.log"

  assert_file_contains "$case_dir/doctor-public-share-long-expiry.log" "public share.default_expires_in is longer than 720h: 1080h"
  assert_file_contains "$case_dir/doctor-public-share-long-expiry.log" "Summary: 0 failure(s)"

  cat > "$case_dir/config-share-unlimited-max-access.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://nas.example.com"
default_expires_in = "168h"
default_max_access = 0
EOF

  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-unlimited-max-access.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-unlimited-max-access.log"

  assert_file_contains "$case_dir/doctor-public-share-unlimited-max-access.log" "public share.default_max_access leaves new share links without an access limit"
  assert_file_contains "$case_dir/doctor-public-share-unlimited-max-access.log" "Summary: 0 failure(s)"

  write_executable "$fake_path/curl" \
    '#!/usr/bin/env bash' \
    'url="${@: -1}"' \
    'headers_file=""' \
    'previous=""' \
    'for arg in "$@"; do' \
    '  if [[ "$previous" == "-D" ]]; then headers_file="$arg"; fi' \
    '  previous="$arg"' \
    'done' \
    'write_share_probe_headers() {' \
    '  [[ -n "$headers_file" && "$headers_file" != "-" ]] || return 0' \
    '  printf "HTTP/2 404\r\nCache-Control: private, no-cache\r\nX-Content-Type-Options: nosniff\r\nReferrer-Policy: no-referrer\r\n\r\n" > "$headers_file"' \
    '}' \
    'if [[ "$*" == *" -X PROPFIND "* && "$url" == "https://nas.example.com/dav/" ]]; then printf "401"; exit 0; fi' \
    'if [[ "$url" == "https://nas.example.com/api/v1/public/shares/mnemonas-doctor-probe" ]]; then write_share_probe_headers; printf "404"; exit 0; fi' \
    'if [[ "$*" == *" -w "* && "$url" == "http://nas.example.com/health" ]]; then printf "301 https://nas.example.com/health"; exit 0; fi' \
    'case "$url" in' \
    '  https://nas.example.com/health) printf "ok\n";;' \
    '  http://nas.example.com:18080/health) exit 7;;' \
    '  */) printf "<div id=\"root\"></div>\n";;' \
    '  *) printf "ok\n";;' \
    'esac'

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-trimmed.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-missing-vary.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted public share JSON without Vary: Cookie"
  assert_file_contains "$case_dir/doctor-public-share-missing-vary.log" "public share API probe reached MnemoNAS"
  assert_file_contains "$case_dir/doctor-public-share-missing-vary.log" "public share JSON response is missing cache/security headers (Vary=Cookie)"

  write_executable "$fake_path/curl" \
    '#!/usr/bin/env bash' \
    'url="${@: -1}"' \
    'headers_file=""' \
    'previous=""' \
    'for arg in "$@"; do' \
    '  if [[ "$previous" == "-D" ]]; then headers_file="$arg"; fi' \
    '  previous="$arg"' \
    'done' \
    'write_share_probe_headers() {' \
    '  [[ -n "$headers_file" && "$headers_file" != "-" ]] || return 0' \
    '  printf "HTTP/2 404\r\nCache-Control: private, no-cache\r\nVary: Cookie\r\nX-Content-Type-Options: nosniff\r\nReferrer-Policy: no-referrer\r\nSet-Cookie: mnemonas_share_probe=unexpected; HttpOnly\r\n\r\n" > "$headers_file"' \
    '}' \
    'if [[ "$*" == *" -X PROPFIND "* && "$url" == "https://nas.example.com/dav/" ]]; then printf "401"; exit 0; fi' \
    'if [[ "$url" == "https://nas.example.com/api/v1/public/shares/mnemonas-doctor-probe" ]]; then write_share_probe_headers; printf "404"; exit 0; fi' \
    'if [[ "$*" == *" -w "* && "$url" == "http://nas.example.com/health" ]]; then printf "301 https://nas.example.com/health"; exit 0; fi' \
    'case "$url" in' \
    '  https://nas.example.com/health) printf "ok\n";;' \
    '  http://nas.example.com:18080/health) exit 7;;' \
    '  */) printf "<div id=\"root\"></div>\n";;' \
    '  *) printf "ok\n";;' \
    'esac'

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-trimmed.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-cookie.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted public share JSON setting cookies on missing share probe"
  assert_file_contains "$case_dir/doctor-public-share-cookie.log" "public share API probe reached MnemoNAS"
  assert_file_contains "$case_dir/doctor-public-share-cookie.log" "public share JSON response is missing cache/security headers (Set-Cookie must be absent on missing-share probes)"

  write_executable "$fake_path/curl" \
    '#!/usr/bin/env bash' \
    'url="${@: -1}"' \
    'if [[ "$*" == *" -X PROPFIND "* && "$url" == "https://nas.example.com/dav/" ]]; then printf "401"; exit 0; fi' \
    'if [[ "$url" == "https://nas.example.com/api/v1/public/shares/mnemonas-doctor-probe" ]]; then printf "403"; exit 0; fi' \
    'if [[ "$*" == *" -w "* && "$url" == "http://nas.example.com/health" ]]; then printf "301 https://nas.example.com/health"; exit 0; fi' \
    'case "$url" in' \
    '  https://nas.example.com/health) printf "ok\n";;' \
    '  http://nas.example.com:18080/health) exit 7;;' \
    '  */) printf "<div id=\"root\"></div>\n";;' \
    '  *) printf "ok\n";;' \
    'esac'

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-trimmed.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-proxy-blocked.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted blocked public share API routing"
  assert_file_contains "$case_dir/doctor-public-share-proxy-blocked.log" "public share API probe was blocked before MnemoNAS share lookup: https://nas.example.com/api/v1/public/shares/mnemonas-doctor-probe (HTTP 403)"
  assert_file_not_contains "$case_dir/doctor-public-share-proxy-blocked.log" "public share JSON response is missing cache/security headers"

  cat > "$case_dir/config-webdav-placeholder-password.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[webdav]
enabled = true
auth_type = "basic"
password = "change-this-webdav-password"
EOF

  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-webdav-placeholder-password.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-webdav-placeholder-password.log"

  assert_file_contains "$case_dir/doctor-public-webdav-placeholder-password.log" "public WebDAV Basic Auth password should be changed before public access (risk: placeholder)"
  assert_file_not_contains "$case_dir/doctor-public-webdav-placeholder-password.log" "change-this-webdav-password"
  assert_file_contains "$case_dir/doctor-public-webdav-placeholder-password.log" "Summary: 0 failure(s)"

  mv "$storage_dir/secrets.json" "$storage_dir/secrets.json.saved"
  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-missing-webdav-secrets.log"
  status=$?
  set -e
  mv "$storage_dir/secrets.json.saved" "$storage_dir/secrets.json"

  [[ "$status" -ne 0 ]] || fail "public doctor accepted missing generated WebDAV password file"
  assert_file_contains "$case_dir/doctor-public-missing-webdav-secrets.log" "public WebDAV generated password file is missing"

  mv "$storage_dir/secrets.json" "$storage_dir/secrets.json.real"
  ln -s "$storage_dir/secrets.json.real" "$storage_dir/secrets.json"
  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-symlink-webdav-secrets.log"
  status=$?
  set -e
  rm -f "$storage_dir/secrets.json"
  mv "$storage_dir/secrets.json.real" "$storage_dir/secrets.json"

  [[ "$status" -ne 0 ]] || fail "public doctor accepted symlink generated WebDAV password file"
  assert_file_contains "$case_dir/doctor-public-symlink-webdav-secrets.log" "public WebDAV generated password file is a symlink"

  local real_webdav_storage_parent="$case_dir/real-webdav-storage"
  local linked_webdav_storage_parent="$case_dir/linked-webdav-storage"
  local linked_webdav_storage_root="$linked_webdav_storage_parent/data"
  mkdir -p "$real_webdav_storage_parent/data/files" "$real_webdav_storage_parent/data/.mnemonas"
  cp "$storage_dir/secrets.json" "$real_webdav_storage_parent/data/secrets.json"
  chmod 0750 "$real_webdav_storage_parent/data" "$real_webdav_storage_parent/data/files"
  chmod 0700 "$real_webdav_storage_parent/data/.mnemonas"
  chmod 0600 "$real_webdav_storage_parent/data/secrets.json"
  ln -s "$real_webdav_storage_parent" "$linked_webdav_storage_parent"
  cat > "$case_dir/config-symlink-webdav-secrets-component.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$linked_webdav_storage_root"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
users_file = "$storage_dir/.mnemonas/users.json"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-symlink-webdav-secrets-component.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-symlink-webdav-secrets-component.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted a symlink WebDAV generated password path component"
  assert_file_contains "$case_dir/doctor-public-symlink-webdav-secrets-component.log" "generated secrets file path contains a symlink component; use a regular private file path: $linked_webdav_storage_parent"
  assert_file_contains "$case_dir/doctor-public-symlink-webdav-secrets-component.log" "public WebDAV generated password file path contains a symlink component; use a regular private file path: $linked_webdav_storage_parent"

  chmod 0644 "$storage_dir/secrets.json"
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-open-webdav-secrets.log"
  chmod 0600 "$storage_dir/secrets.json"

  assert_file_contains "$case_dir/doctor-public-open-webdav-secrets.log" "public WebDAV generated password file is not private"
  assert_file_contains "$case_dir/doctor-public-open-webdav-secrets.log" "Summary: 0 failure(s)"

  cp "$storage_dir/secrets.json" "$storage_dir/secrets.json.saved"
  cat > "$storage_dir/secrets.json" <<'EOF'
{"jwt_secret":"test-jwt-secret-value-with-enough-length","webdav_password":"password123"}
EOF
  chmod 0600 "$storage_dir/secrets.json"
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-weak-generated-webdav-password.log"
  mv "$storage_dir/secrets.json.saved" "$storage_dir/secrets.json"

  assert_file_contains "$case_dir/doctor-public-weak-generated-webdav-password.log" "public WebDAV generated Basic Auth password should be changed before public access (risk: placeholder)"
  assert_file_not_contains "$case_dir/doctor-public-weak-generated-webdav-password.log" "password123"
  assert_file_contains "$case_dir/doctor-public-weak-generated-webdav-password.log" "Summary: 0 failure(s)"

  cat > "$case_dir/config-webdav-empty-prefix.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[webdav]
enabled = true
prefix = ""
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-webdav-empty-prefix.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-webdav-empty-prefix.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted empty WebDAV prefix"
  assert_file_contains "$case_dir/doctor-public-webdav-empty-prefix.log" "public WebDAV prefix is invalid: <empty>"

  cat > "$case_dir/config-webdav-reserved-prefix.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[webdav]
enabled = true
prefix = "/api/v1"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-webdav-reserved-prefix.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-webdav-reserved-prefix.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted reserved WebDAV prefix"
  assert_file_contains "$case_dir/doctor-public-webdav-reserved-prefix.log" "public WebDAV prefix is invalid: /api/v1"

  cat > "$case_dir/config-webdav-spaced-prefix.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[webdav]
enabled = true
prefix = "/team /sub"
EOF

  write_executable "$fake_path/curl" \
    '#!/usr/bin/env bash' \
    'url="${@: -1}"' \
    'if [[ "$*" == *" -X PROPFIND "* && "$url" == "https://nas.example.com/team%20/sub/" ]]; then printf "401"; exit 0; fi' \
    'if [[ "$*" == *" -w "* && "$url" == "http://nas.example.com/health" ]]; then printf "301 https://nas.example.com/health"; exit 0; fi' \
    'case "$url" in' \
    '  https://nas.example.com/health) printf "ok\n";;' \
    '  http://nas.example.com:18080/health) exit 7;;' \
    '  */) printf "<div id=\"root\"></div>\n";;' \
    '  *) printf "ok\n";;' \
    'esac'

  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-webdav-spaced-prefix.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-webdav-spaced-prefix.log"

  assert_file_contains "$case_dir/doctor-public-webdav-spaced-prefix.log" "public WebDAV anonymous PROPFIND is rejected: https://nas.example.com/team%20/sub/ (HTTP 401)"
  assert_file_contains "$case_dir/doctor-public-webdav-spaced-prefix.log" "Summary: 0 failure(s)"

  write_executable "$fake_path/curl" \
    '#!/usr/bin/env bash' \
    'url="${@: -1}"' \
    'if [[ "$*" == *" -X PROPFIND "* && "$url" == "https://nas.example.com/dav/" ]]; then printf "207"; exit 0; fi' \
    'if [[ "$*" == *" -w "* && "$url" == "http://nas.example.com/health" ]]; then printf "301 https://nas.example.com/health"; exit 0; fi' \
    'case "$url" in' \
    '  https://nas.example.com/health) printf "ok\n";;' \
    '  http://nas.example.com:18080/health) exit 7;;' \
    '  */) printf "<div id=\"root\"></div>\n";;' \
    '  *) printf "ok\n";;' \
    'esac'

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-webdav-open.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted anonymous WebDAV PROPFIND"
  assert_file_contains "$case_dir/doctor-public-webdav-open.log" "public WebDAV allows anonymous PROPFIND at https://nas.example.com/dav/ (HTTP 207)"

  write_executable "$fake_path/curl" \
    '#!/usr/bin/env bash' \
    'url="${@: -1}"' \
    'if [[ "$*" == *" -w "* && "$url" == "http://nas.example.com/health" ]]; then printf "301 https://nas.example.com.evil/health"; exit 0; fi' \
    'case "$url" in' \
    '  https://nas.example.com/health) printf "ok\n";;' \
    '  http://nas.example.com:18080/health) exit 7;;' \
    '  */) printf "<div id=\"root\"></div>\n";;' \
    '  *) printf "ok\n";;' \
    'esac'

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-bad-redirect.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted HTTP redirect to the wrong domain"
  assert_file_contains "$case_dir/doctor-public-bad-redirect.log" "public HTTP does not clearly redirect to HTTPS"

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    MNEMONAS_FAKE_CERT_EXPIRES_SOON=1 \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-expiring-cert.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted expiring public HTTPS certificate"
  assert_file_contains "$case_dir/doctor-public-expiring-cert.log" "public HTTPS certificate expires within 30 days or cannot be parsed"
  assert_file_contains "$case_dir/doctor-public-expiring-cert.log" "certificate failure triage for nas.example.com"

  write_executable "$fake_path/curl" \
    '#!/usr/bin/env bash' \
    'url="${@: -1}"' \
    'if [[ "$*" == *" -w "* && "$url" == "http://nas.example.com/health" ]]; then printf "301 https://nas.example.com/health"; exit 0; fi' \
    'if [[ "$*" == *" -w "* && "$url" == "http://nas.example.com:18080/health" ]]; then printf "404"; exit 0; fi' \
    'case "$url" in' \
    '  https://nas.example.com/health) printf "ok\n";;' \
    '  http://nas.example.com:18080/health) exit 22;;' \
    '  */) printf "<div id=\"root\"></div>\n";;' \
    '  *) printf "ok\n";;' \
    'esac'

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-direct-404.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted direct control plane HTTP 404 exposure"
  assert_file_contains "$case_dir/doctor-public-direct-404.log" "public direct control plane is publicly reachable"

  write_executable "$fake_path/curl" \
    '#!/usr/bin/env bash' \
    'url="${@: -1}"' \
    'if [[ "$*" == *" -X PROPFIND "* && "$url" == "https://nas.example.com/dav/" ]]; then printf "401"; exit 0; fi' \
    'if [[ "$*" == *" -w "* && "$url" == "http://nas.example.com/health" ]]; then printf "301 https://nas.example.com/health"; exit 0; fi' \
    'case "$url" in' \
    '  https://nas.example.com/health) printf "ok\n";;' \
    '  http://nas.example.com:18080/health) exit 7;;' \
    '  */) printf "<div id=\"root\"></div>\n";;' \
    '  *) printf "ok\n";;' \
    'esac'

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    MNEMONAS_FAKE_PUBLIC_CONTROL_TCP_OPEN=1 \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-direct-tcp-open.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted direct control plane TCP exposure"
  assert_file_contains "$case_dir/doctor-public-direct-tcp-open.log" "public direct control plane is not publicly reachable: http://nas.example.com:18080/health"
  assert_file_contains "$case_dir/doctor-public-direct-tcp-open.log" "public direct control plane TCP port 18080 is publicly reachable on nas.example.com"

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    MNEMONAS_FAKE_PUBLIC_DATAPLANE_GRPC_TCP_OPEN=1 \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-dataplane-grpc-tcp-open.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted public dataplane gRPC TCP exposure"
  assert_file_contains "$case_dir/doctor-public-dataplane-grpc-tcp-open.log" "public dataplane gRPC port 19090 is publicly reachable on nas.example.com"

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    MNEMONAS_FAKE_PUBLIC_DATAPLANE_HTTP_TCP_OPEN=1 \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-dataplane-http-tcp-open.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted public dataplane HTTP TCP exposure"
  assert_file_contains "$case_dir/doctor-public-dataplane-http-tcp-open.log" "public dataplane HTTP port 19091 is publicly reachable on nas.example.com"

  write_executable "$fake_path/curl" \
    '#!/usr/bin/env bash' \
    'url="${@: -1}"' \
    'if [[ "$*" == *" -w "* && "$url" == "http://nas.example.com/health" ]]; then printf "301 https://nas.example.com/health"; exit 0; fi' \
    'if [[ "$*" == *" -w "* && "$url" == "http://nas.example.com:18080/health" ]]; then printf "200"; exit 0; fi' \
    'case "$url" in' \
    '  https://nas.example.com/health) printf "ok\n";;' \
    '  http://nas.example.com:18080/health) printf "open\n";;' \
    '  */) printf "<div id=\"root\"></div>\n";;' \
    '  *) printf "ok\n";;' \
    'esac'
  cat > "$case_dir/config-unsafe.toml" <<EOF
[server]
host = "0.0.0.0"
port = 18080
trusted_proxy_hops = 0

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"
EOF
  touch "$storage_dir/.mnemonas/initial-password.txt"

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-unsafe.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-unsafe.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted unsafe public deployment"
  assert_file_contains "$case_dir/doctor-public-unsafe.log" "public backend host should be 127.0.0.1"
  assert_file_contains "$case_dir/doctor-public-unsafe.log" "server.trusted_proxy_hops should be at least 1"
  assert_file_contains "$case_dir/doctor-public-unsafe.log" "public direct control plane is publicly reachable"
  assert_file_contains "$case_dir/doctor-public-unsafe.log" "initial admin password file still exists"

  write_executable "$fake_path/curl" \
    '#!/usr/bin/env bash' \
    'url="${@: -1}"' \
    'if [[ "$*" == *" -w "* && "$url" == "http://nas.example.com/health" ]]; then printf "301 https://nas.example.com/health"; exit 0; fi' \
    'case "$url" in' \
    '  https://nas.example.com/health) printf "ok\n";;' \
    '  http://nas.example.com:18080/health) exit 7;;' \
    '  */) printf "<div id=\"root\"></div>\n";;' \
    '  *) printf "ok\n";;' \
    'esac'
  rm -f "$storage_dir/.mnemonas/initial-password.txt"
  cat > "$case_dir/config-no-auth.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
enabled = false

[webdav]
enabled = true
auth_type = "none"

[security]
allow_unsafe_no_auth = true
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-no-auth.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-no-auth.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted disabled authentication"
  assert_file_contains "$case_dir/doctor-public-no-auth.log" "public auth.enabled must remain true"
  assert_file_contains "$case_dir/doctor-public-no-auth.log" "security.allow_unsafe_no_auth must be false for public deployments"
  assert_file_contains "$case_dir/doctor-public-no-auth.log" "public WebDAV must not use auth_type=none"

  local no_curl_path="$case_dir/no-curl-bin"
  make_doctor_path_without_curl "$fake_path" "$no_curl_path"

  set +e
  PATH="$no_curl_path" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-no-curl.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted missing curl"
  assert_file_contains "$case_dir/doctor-public-no-curl.log" "curl is required for public diagnostics"

  local no_python_path="$case_dir/no-python-bin"
  make_doctor_path_without_python "$fake_path" "$no_python_path"

  set +e
  PATH="$no_python_path" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-no-python.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted missing python3"
  assert_file_contains "$case_dir/doctor-public-no-python.log" "python3 is required for public diagnostics"

  local no_openssl_path="$case_dir/no-openssl-bin"
  make_doctor_path_without_openssl "$fake_path" "$no_openssl_path"

  set +e
  PATH="$no_openssl_path" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-no-openssl.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted missing openssl"
  assert_file_contains "$case_dir/doctor-public-no-openssl.log" "openssl is required for public HTTPS certificate checks"

  local no_getent_path="$case_dir/no-getent-bin"
  make_doctor_path_without_getent "$fake_path" "$no_getent_path"

  set +e
  PATH="$no_getent_path" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-no-getent.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted missing getent"
  assert_file_contains "$case_dir/doctor-public-no-getent.log" "getent is required for public diagnostics"

  write_executable "$fake_path/getent" '#!/usr/bin/env bash' 'exit 1'

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-dns-missing.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted a public domain without local DNS resolution"
  assert_file_contains "$case_dir/doctor-public-dns-missing.log" "public domain does not resolve locally: nas.example.com"

  write_executable "$fake_path/getent" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "ahosts" && "${2:-}" == "nas.example.com" ]]; then printf "203.0.113.10 STREAM nas.example.com\n"; exit 0; fi' \
    'exit 1'

  cat > "$case_dir/config-dotted-no-auth.toml" <<EOF
auth.enabled = false
webdav.enabled = true
webdav.auth_type = "none"
security.allow_unsafe_no_auth = true

[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"
EOF

  set +e
  PATH="$no_python_path" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-dotted-no-auth.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-dotted-no-auth-no-python.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted dotted-key disabled authentication without python3"
  assert_file_contains "$case_dir/doctor-public-dotted-no-auth-no-python.log" "public auth.enabled must remain true"
  assert_file_contains "$case_dir/doctor-public-dotted-no-auth-no-python.log" "security.allow_unsafe_no_auth must be false for public deployments"
  assert_file_contains "$case_dir/doctor-public-dotted-no-auth-no-python.log" "public WebDAV must not use auth_type=none"

  cat > "$case_dir/config-webdav-spaced-none.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[webdav]
enabled = true
auth_type = " none "
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-webdav-spaced-none.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-webdav-spaced-none.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted whitespace-padded WebDAV auth_type=none"
  assert_file_contains "$case_dir/doctor-public-webdav-spaced-none.log" "public WebDAV must not use auth_type=none"

  cat > "$case_dir/config-zero-auth-access-ttl.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
access_token_ttl = "0"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-zero-auth-access-ttl.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-zero-auth-access-ttl.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted zero auth access token TTL"
  assert_file_contains "$case_dir/doctor-public-zero-auth-access-ttl.log" "public auth.access_token_ttl must be a positive duration"

  cat > "$case_dir/config-negative-auth-refresh-ttl.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
refresh_token_ttl = "-1h"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-negative-auth-refresh-ttl.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-negative-auth-refresh-ttl.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted negative auth refresh token TTL"
  assert_file_contains "$case_dir/doctor-public-negative-auth-refresh-ttl.log" "public auth.refresh_token_ttl must be a positive duration"

  cat > "$case_dir/config-share-negative-expiry.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://nas.example.com"
default_expires_in = "-1h"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-negative-expiry.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-negative-expiry.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted negative share default expiry"
  assert_file_contains "$case_dir/doctor-public-share-negative-expiry.log" "public share.default_expires_in must be empty, 0, or a non-negative duration"

  cat > "$case_dir/config-share-negative-max-access.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://nas.example.com"
default_max_access = -1
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-negative-max-access.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-negative-max-access.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted negative share default max access"
  assert_file_contains "$case_dir/doctor-public-share-negative-max-access.log" "public share.default_max_access must be zero or greater"

  cat > "$case_dir/config-share-http.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "http://nas.example.com"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-http.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-http.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted HTTP share base URL"
  assert_file_contains "$case_dir/doctor-public-share-http.log" "public share.base_url must use https"

  cat > "$case_dir/config-share-non-default-port.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://nas.example.com:8443"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-non-default-port.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-non-default-port.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted share base URL with a non-default HTTPS port"
  assert_file_contains "$case_dir/doctor-public-share-non-default-port.log" "public share.base_url must use the HTTPS default port 443"

  cat > "$case_dir/config-share-nonnumeric-port.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://nas.example.com:abc"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-nonnumeric-port.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-nonnumeric-port.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted share base URL with a non-numeric HTTPS port"
  assert_file_contains "$case_dir/doctor-public-share-nonnumeric-port.log" "public share.base_url host is invalid"

  cat > "$case_dir/config-share-empty-port.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://nas.example.com:"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-empty-port.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-empty-port.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted share base URL with an empty HTTPS port"
  assert_file_contains "$case_dir/doctor-public-share-empty-port.log" "public share.base_url host is invalid"

  cat > "$case_dir/config-share-userinfo.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://operator@nas.example.com"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-userinfo.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-userinfo.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted share base URL with userinfo"
  assert_file_contains "$case_dir/doctor-public-share-userinfo.log" "public share.base_url must not include userinfo"

  cat > "$case_dir/config-share-query.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://nas.example.com?token=secret"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-query.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-query.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted share base URL with query"
  assert_file_contains "$case_dir/doctor-public-share-query.log" "public share.base_url must not include query or fragment"

  cat > "$case_dir/config-share-empty-query.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://nas.example.com?"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-empty-query.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-empty-query.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted share base URL with empty query marker"
  assert_file_contains "$case_dir/doctor-public-share-empty-query.log" "public share.base_url must not include query or fragment"

  cat > "$case_dir/config-share-fragment.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://nas.example.com#share"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-fragment.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-fragment.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted share base URL with fragment"
  assert_file_contains "$case_dir/doctor-public-share-fragment.log" "public share.base_url must not include query or fragment"

  cat > "$case_dir/config-share-empty-fragment.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://nas.example.com#"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-empty-fragment.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-empty-fragment.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted share base URL with empty fragment marker"
  assert_file_contains "$case_dir/doctor-public-share-empty-fragment.log" "public share.base_url must not include query or fragment"

  cat > "$case_dir/config-share-invalid-host.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://nas..example.com"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-invalid-host.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-invalid-host.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted share base URL with an invalid host"
  assert_file_contains "$case_dir/doctor-public-share-invalid-host.log" "public share.base_url host is invalid"

  cat > "$case_dir/config-share-unbracketed-ipv6.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://2001:db8::1"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-unbracketed-ipv6.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-unbracketed-ipv6.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted share base URL with an unbracketed IPv6 host"
  assert_file_contains "$case_dir/doctor-public-share-unbracketed-ipv6.log" "public share.base_url host is invalid"

  cat > "$case_dir/config-share-invalid-bracketed-ipv6.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://[::::]"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-invalid-bracketed-ipv6.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-invalid-bracketed-ipv6.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted share base URL with an invalid bracketed IPv6 host"
  assert_file_contains "$case_dir/doctor-public-share-invalid-bracketed-ipv6.log" "public share.base_url host is invalid"

  cat > "$case_dir/config-share-dot-segment.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[share]
enabled = true
base_url = "https://nas.example.com/shares/%2e%2e/team"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-share-dot-segment.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-share-dot-segment.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted share base URL with a dot segment path"
  assert_file_contains "$case_dir/doctor-public-share-dot-segment.log" "public share.base_url path must not contain . or .. segments"

  cat > "$case_dir/config-missing-users.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
users_file = "$case_dir/missing-users.json"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-missing-users.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-missing-users.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted a missing users file"
  assert_file_contains "$case_dir/doctor-public-missing-users.log" "public users file is missing; cannot verify administrator redundancy"

  printf '{not-json\n' > "$case_dir/invalid-users.json"
  cat > "$case_dir/config-invalid-users.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
users_file = "$case_dir/invalid-users.json"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-invalid-users.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-invalid-users.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted an invalid users file"
  assert_file_contains "$case_dir/doctor-public-invalid-users.log" "public users file could not be parsed; cannot verify administrator redundancy"
  assert_file_contains "$case_dir/doctor-public-invalid-users.log" "users.json parse error"

  cat > "$case_dir/malformed-users.json" <<EOF
[
  {"id":"admin-1","username":"admin","password_hash":"$fake_admin_hash","role":"admin","disabled":false},
  {"id":"admin-1","username":"backup-admin","password_hash":"$fake_admin_hash","role":"admin","disabled":false}
]
EOF
  cat > "$case_dir/config-malformed-users.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
users_file = "$case_dir/malformed-users.json"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-malformed-users.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-malformed-users.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted a malformed users file"
  assert_file_contains "$case_dir/doctor-public-malformed-users.log" "public users file could not be parsed; cannot verify administrator redundancy"
  assert_file_contains "$case_dir/doctor-public-malformed-users.log" "duplicate user id"

  cat > "$case_dir/unusable-admin-users.json" <<EOF
[
  {"id":"admin-1","username":"admin","role":"admin","disabled":false},
  {"id":"admin-2","username":"backup-admin","password_hash":"not-bcrypt","role":"admin","disabled":false}
]
EOF
  cat > "$case_dir/config-unusable-admin-users.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
users_file = "$case_dir/unusable-admin-users.json"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-unusable-admin-users.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-unusable-admin-users.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted enabled administrators without usable password hashes"
  assert_file_contains "$case_dir/doctor-public-unusable-admin-users.log" "public users file could not be parsed; cannot verify administrator redundancy"
  assert_file_contains "$case_dir/doctor-public-unusable-admin-users.log" "invalid password_hash for enabled administrator"

  cat > "$case_dir/zero-admin-users.json" <<'EOF'
[
  {"id":"disabled-admin","username":"disabled-admin","role":"admin","disabled":true},
  {"id":"user-1","username":"user","role":"user","disabled":false}
]
EOF
  cat > "$case_dir/config-zero-admin.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
users_file = "$case_dir/zero-admin-users.json"
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-zero-admin.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-zero-admin.log"
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "public doctor accepted zero enabled administrators"
  assert_file_contains "$case_dir/doctor-public-zero-admin.log" "public users file has no enabled administrators"

  cat > "$case_dir/single-admin-users.json" <<EOF
[
  {"id":"admin-1","username":"admin","password_hash":"$fake_admin_hash","role":"admin","disabled":false},
  {"id":"disabled-admin","username":"disabled-admin","role":"admin","disabled":true},
  {"id":"user-1","username":"user","role":"user","disabled":false}
]
EOF
  cat > "$case_dir/config-single-admin.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080
trusted_proxy_hops = 1

[storage]
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"

[auth]
users_file = "$case_dir/single-admin-users.json"
EOF

  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-single-admin.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-single-admin.log"

  assert_file_contains "$case_dir/doctor-public-single-admin.log" "public administrator redundancy is weak: only one enabled administrator"
}

run_doctor_input_validation_test() {
  local case_dir="$TMP_ROOT/doctor-input-validation"
  local status
  mkdir -p "$case_dir"

  expect_doctor_public_domain_failure() {
    local name="$1"
    local domain="$2"
    local expected="$3"
    local output="$case_dir/$name.log"
    local status

    set +e
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain "$domain" > "$output" 2>&1
    status=$?
    set -e

    [[ "$status" -ne 0 ]] || fail "doctor accepted PUBLIC_DOMAIN for $name"
    assert_file_contains "$output" "$expected"
  }

  set +e
  SERVER_PORT="8080 bad" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/server-port.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "doctor accepted SERVER_PORT with whitespace"
  assert_file_contains "$case_dir/server-port.log" "SERVER_PORT cannot contain whitespace"

  set +e
  SERVER_PORT="8080"$'\a' \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/server-port-control.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "doctor accepted SERVER_PORT with control character"
  assert_file_contains "$case_dir/server-port-control.log" "SERVER_PORT cannot contain control characters"

  set +e
  DATAPLANE_GRPC_ADDR="127.0.0.1:70000" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/grpc-addr.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "doctor accepted invalid DATAPLANE_GRPC_ADDR port"
  assert_file_contains "$case_dir/grpc-addr.log" "DATAPLANE_GRPC_ADDR port must be between 1 and 65535"

  set +e
  SERVER_URL="file:///etc/passwd" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/server-url.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "doctor accepted a non-http SERVER_URL"
  assert_file_contains "$case_dir/server-url.log" "SERVER_URL must be an http(s) URL"

  set +e
  SERVER_URL="http://127.0.0.1:8080"$'\a' \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/server-url-control.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "doctor accepted SERVER_URL with control character"
  assert_file_contains "$case_dir/server-url-control.log" "SERVER_URL cannot contain control characters"

  expect_doctor_public_domain_failure "public-domain-scheme" "https://nas.example.com" "PUBLIC_DOMAIN must be a hostname without scheme or port"
  expect_doctor_public_domain_failure "public-domain-control" "nas.example.com"$'\a' "PUBLIC_DOMAIN cannot contain control characters"
  expect_doctor_public_domain_failure "public-domain-localhost" "localhost" "PUBLIC_DOMAIN must be a fully qualified hostname"
  expect_doctor_public_domain_failure "public-domain-ip" "127.0.0.1" "PUBLIC_DOMAIN must be a hostname, not an IP address"
  expect_doctor_public_domain_failure "public-domain-ipv4-like-overrange" "999.999.999.999" "PUBLIC_DOMAIN must be a hostname, not an IP address"
  expect_doctor_public_domain_failure "public-domain-leading-label-hyphen" "nas.-example.com" "PUBLIC_DOMAIN is invalid"
  expect_doctor_public_domain_failure "public-domain-trailing-label-hyphen" "nas.example-.com" "PUBLIC_DOMAIN is invalid"
  expect_doctor_public_domain_failure "public-domain-long-label" "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.example.com" "PUBLIC_DOMAIN is invalid"
  expect_doctor_public_domain_failure "public-domain-repeated-trailing-dot" "nas.example.com.." "PUBLIC_DOMAIN is invalid"
  expect_doctor_public_domain_failure "public-domain-empty-label" "nas..example.com" "PUBLIC_DOMAIN is invalid"
}

run_doctor_invalid_toml_syntax_test() {
  local case_dir="$TMP_ROOT/doctor-invalid-toml"
  local fake_path="$case_dir/fake-bin"
  local bin_dir="$case_dir/bin"
  local web_dir="$case_dir/web"
  local storage_dir="$case_dir/storage"
  local backup_dir="$case_dir/backup"
  local status
  mkdir -p "$fake_path" "$bin_dir" "$web_dir" "$storage_dir/files" "$storage_dir/.mnemonas" "$backup_dir"
  chmod 0750 "$storage_dir" "$storage_dir/files"
  chmod 0700 "$storage_dir/.mnemonas"

  write_executable "$bin_dir/nasd" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "--check-config" ]]; then exit 0; fi' \
    'exit 0'
  write_executable "$bin_dir/dataplane" '#!/usr/bin/env bash' 'exit 0'
  printf '<div id="root"></div>\n' > "$web_dir/index.html"
  write_executable "$fake_path/id" '#!/usr/bin/env bash' 'exit 0'
  write_executable "$fake_path/getent" '#!/usr/bin/env bash' 'exit 1'
  write_executable "$fake_path/systemctl" '#!/usr/bin/env bash' 'exit 0'
  write_executable "$fake_path/curl" \
    '#!/usr/bin/env bash' \
    'url="${@: -1}"' \
    'if [[ "$url" == */ ]]; then printf "<div id=\"root\"></div>\n"; else printf "ok\n"; fi'
  write_executable "$fake_path/ss" \
    '#!/usr/bin/env bash' \
    'printf "LISTEN 0 4096 127.0.0.1:18080 0.0.0.0:*\n"' \
    'printf "LISTEN 0 4096 127.0.0.1:19090 0.0.0.0:*\n"' \
    'printf "LISTEN 0 4096 127.0.0.1:19091 0.0.0.0:*\n"'
  write_executable "$fake_path/findmnt" \
    '#!/usr/bin/env bash' \
    'printf "tank/data zfs %s\n" "${@: -1}"'
  write_executable "$fake_path/df" \
    '#!/usr/bin/env bash' \
    'printf "Filesystem 1024-blocks Used Available Capacity Mounted on\n"' \
    'printf "/dev/fake 20971520 5242880 15728640 25%% %s\n" "${@: -1}"'

  cat > "$case_dir/config-invalid.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080

[broken
EOF

  set +e
  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config-invalid.toml" \
    STORAGE_ROOT="$storage_dir" \
    DATAPLANE_GRPC_ADDR="127.0.0.1:19090" \
    DATAPLANE_HTTP_ADDR="127.0.0.1:19091" \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/doctor-invalid-toml.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "doctor accepted an invalid TOML config when nasd --check-config succeeded"
  assert_file_contains "$case_dir/doctor-invalid-toml.log" "config TOML syntax is invalid: $case_dir/config-invalid.toml"
  assert_file_contains "$case_dir/doctor-invalid-toml.log" "TOML parse error:"
  assert_file_contains "$case_dir/doctor-invalid-toml.log" "skipping nasd --check-config because config TOML syntax is invalid"
  assert_file_not_contains "$case_dir/doctor-invalid-toml.log" "config validates"
}

run_fresh_install_test
run_storage_ownership_repair_is_explicit_test
run_web_install_preserves_share_sibling_permissions_test
run_web_install_preserves_existing_assets_on_copy_failure_test
run_install_preserves_existing_runtime_on_config_check_failure_test
run_install_removes_new_config_on_config_check_failure_test
run_install_preserves_existing_runtime_on_binary_install_failure_test
run_install_rolls_back_late_binary_move_failure_test
run_install_reports_service_restart_failure_test
run_install_reports_daemon_reload_failure_test
run_install_reports_service_enable_failure_test
run_successful_upgrade_preserves_config_and_data_test
run_systemd_release_rollback_preserves_config_and_data_test
run_source_checkout_stale_binary_test
run_existing_config_test
run_invalid_input_test
run_service_account_validation_test
run_server_host_validation_test
run_server_port_normalization_test
run_protected_web_dir_test
run_share_dir_overlap_test
run_web_dir_overlap_test
run_core_path_overlap_test
run_protected_config_and_storage_test
run_config_path_scope_test
run_systemd_specifier_rejection_test
run_symlink_path_rejection_test
run_storage_subdir_symlink_rejection_test
run_systemd_newline_rejection_test
run_systemd_control_character_rejection_test
run_dataplane_addr_validation_test
run_doctor_config_test
run_doctor_public_domain_test
run_doctor_input_validation_test
run_doctor_invalid_toml_syntax_test

printf '[systemd-install-test] all checks passed\n'
