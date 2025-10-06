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
  assert_file_contains "$case_dir/install.log" "Read initial password: sudo cat $storage_dir/.mnemonas/initial-password.txt"
  assert_file_contains "$case_dir/install.log" "Configure public HTTPS: sudo $install_dir/bin/mnemonas-public-setup --proxy caddy <domain> <email>"
  assert_file_contains "$case_dir/install.log" "Uninstall: sudo $install_dir/bin/mnemonas-uninstall-systemd"
  assert_mode "$storage_dir" "750"
  assert_mode "$storage_dir/files" "750"
  assert_mode "$storage_dir/.mnemonas" "700"
  assert_mode "$storage_dir/.mnemonas/objects" "700"
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
  mkdir -p "$install_dir/etc/mnemonas" "$storage_dir"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"

  cat > "$install_dir/etc/mnemonas/config.toml" <<EOF
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
EOF

  PATH="$fake_path:$PATH" \
    RELEASE_DIR="$release_dir" \
    BIN_DIR="$install_dir/bin" \
    SHARE_DIR="$install_dir/share/mnemonas" \
    CONFIG_DIR="$install_dir/etc/mnemonas" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
    SYSTEMD_DIR="$install_dir/systemd" \
    ENABLE_NOW=0 \
    "$REPO_ROOT/scripts/install-systemd.sh" > "$case_dir/install.log"

  assert_file_contains "$case_dir/install.log" "Open Web UI: http://127.0.0.1:18080"
  assert_file_contains "$install_dir/systemd/mnemonas-dataplane.service" "Environment=DATAPLANE_GRPC_ADDR=127.0.0.1:19090"
  assert_file_contains "$install_dir/systemd/mnemonas-dataplane.service" "Environment=DATAPLANE_DATA_DIR=$storage_dir/.mnemonas/objects"
  assert_file_contains "$install_dir/systemd/mnemonas.service" "Environment=DATAPLANE_HTTP_ADDR=127.0.0.1:9091"
  assert_file_contains "$install_dir/systemd/mnemonas.service" "ReadWritePaths=$storage_dir $install_dir/etc/mnemonas"

  write_executable "$case_dir/capture-dataplane" \
    '#!/usr/bin/env bash' \
    'printf "%s\n" "$*" > "$CAPTURE_FILE"'
  CAPTURE_FILE="$case_dir/dataplane.args" \
    CONFIG_PATH="$install_dir/etc/mnemonas/config.toml" \
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
  mkdir -p "$fake_path" "$bin_dir" "$web_dir" "$storage_dir/.mnemonas" "$backup_dir" "$systemd_dir"
  chmod 0750 "$storage_dir"
  chmod 0700 "$storage_dir/.mnemonas"

  write_executable "$bin_dir/nasd" \
    '#!/usr/bin/env bash' \
    'if [[ "${1:-}" == "--check-config" ]]; then exit 0; fi' \
    'exit 0'
  write_executable "$bin_dir/dataplane" '#!/usr/bin/env bash' 'exit 0'
  printf '<div id="root"></div>\n' > "$web_dir/index.html"

  write_executable "$fake_path/id" '#!/usr/bin/env bash' 'exit 0'
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
    'printf "tank/data zfs %s\n" "${@: -1}"'
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
  assert_file_contains "$case_dir/doctor.log" "storage disk space: 15.0 GiB available / 20.0 GiB total (25% used)"
  assert_file_contains "$case_dir/doctor.log" "ufw is active"
  assert_file_contains "$case_dir/doctor.log" "Summary: 0 failure(s)"

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
  write_executable "$fake_path/getent" '#!/usr/bin/env bash' 'exit 1'
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
port = 018080
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
  assert_file_contains "$case_dir/doctor-public.log" "public backend host is loopback-only: 127.0.0.1"
  assert_file_contains "$case_dir/doctor-public.log" "trusted proxy hops configured: 1"
  assert_file_contains "$case_dir/doctor-public.log" "public HTTPS health reachable: https://nas.example.com/health"
  assert_file_contains "$case_dir/doctor-public.log" "public HTTP redirects to HTTPS: http://nas.example.com/health -> https://nas.example.com/health"
  assert_file_contains "$case_dir/doctor-public.log" "public HTTPS certificate matches nas.example.com"
  assert_file_contains "$case_dir/doctor-public.log" "public HTTPS certificate is valid for at least 30 days"
  assert_file_contains "$case_dir/doctor-public.log" "certificate automation detected: Caddy"
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
  assert_file_contains "$case_dir/doctor-public.log" "control plane port 18080 is loopback-only"
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
  assert_file_contains "$case_dir/doctor-public-normalized-domain.log" "public HTTPS health reachable: https://nas.example.com/health"
  assert_file_contains "$case_dir/doctor-public-normalized-domain.log" "public HTTP redirects to HTTPS: http://nas.example.com/health -> https://nas.example.com/health"
  assert_file_contains "$case_dir/doctor-public-normalized-domain.log" "Summary: 0 failure(s)"

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

  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain nas.example.com > "$case_dir/doctor-public-bad-redirect.log"

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

  set +e
  "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain https://nas.example.com > "$case_dir/public-domain.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "doctor accepted PUBLIC_DOMAIN with scheme"
  assert_file_contains "$case_dir/public-domain.log" "PUBLIC_DOMAIN must be a hostname without scheme or port"

  set +e
  "$REPO_ROOT/scripts/mnemonas-doctor.sh" --public-domain "nas.example.com"$'\a' > "$case_dir/public-domain-control.log" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "doctor accepted PUBLIC_DOMAIN with control character"
  assert_file_contains "$case_dir/public-domain-control.log" "PUBLIC_DOMAIN cannot contain control characters"
}

run_fresh_install_test
run_web_install_preserves_share_sibling_permissions_test
run_web_install_preserves_existing_assets_on_copy_failure_test
run_install_preserves_existing_runtime_on_config_check_failure_test
run_install_removes_new_config_on_config_check_failure_test
run_install_preserves_existing_runtime_on_binary_install_failure_test
run_install_reports_service_restart_failure_test
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

printf '[systemd-install-test] all checks passed\n'
