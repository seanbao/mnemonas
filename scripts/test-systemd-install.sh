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
  cp "$REPO_ROOT/scripts/uninstall-systemd.sh" "$dir/scripts/uninstall-systemd.sh"
}

assert_file_contains() {
  local path="$1"
  local expected="$2"
  grep -Fq -- "$expected" "$path" || fail "$path does not contain: $expected"
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
  assert_file_contains "$install_dir/systemd/mnemonas.service" "RequiresMountsFor=$storage_dir"
  assert_file_contains "$install_dir/systemd/mnemonas.service" "CapabilityBoundingSet="
  assert_file_contains "$install_dir/systemd/mnemonas.service" "RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6"
  assert_file_contains "$case_dir/install.log" "Next steps:"
  assert_file_contains "$case_dir/install.log" "Read initial password: sudo cat $storage_dir/.mnemonas/initial-password.txt"
  assert_file_contains "$case_dir/install.log" "Uninstall: sudo $install_dir/bin/mnemonas-uninstall-systemd"
  assert_mode "$storage_dir" "750"
  assert_mode "$storage_dir/files" "750"
  assert_mode "$storage_dir/.mnemonas" "700"
  assert_mode "$storage_dir/.mnemonas/objects" "700"
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
root = "$storage_dir" # keep hashes inside quoted values

[ dataplane ] # dataplane endpoint
grpc_address = '127.0.0.1:19090'

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
  local storage_dir="$case_dir/storage"
  local backup_dir="$case_dir/backup"
  mkdir -p "$fake_path" "$bin_dir" "$web_dir" "$storage_dir/.mnemonas" "$backup_dir"
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
root = "$storage_dir"

[dataplane]
grpc_address = "127.0.0.1:19090"
EOF

  PATH="$fake_path:$PATH" \
    BIN_DIR="$bin_dir" \
    WEB_DIR="$web_dir" \
    CONFIG_PATH="$case_dir/config.toml" \
    DATAPLANE_HTTP_PORT=19091 \
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
    DATAPLANE_HTTP_PORT=19091 \
    BACKUP_ROOT="$backup_dir" \
    "$REPO_ROOT/scripts/mnemonas-doctor.sh" > "$case_dir/doctor-unsafe.log"

  assert_file_contains "$case_dir/doctor-unsafe.log" "dataplane gRPC port 19090 is listening beyond loopback (0.0.0.0:19090)"
  assert_file_contains "$case_dir/doctor-unsafe.log" "dataplane HTTP port 19091 is listening beyond loopback ([::]:19091)"
  assert_file_contains "$case_dir/doctor-unsafe.log" "ufw appears to allow dataplane gRPC port 19090"
  assert_file_contains "$case_dir/doctor-unsafe.log" "ufw appears to allow dataplane HTTP port 19091"
  assert_file_contains "$case_dir/doctor-unsafe.log" "Summary: 0 failure(s), 4 warning(s)"
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
}

run_fresh_install_test
run_existing_config_test
run_invalid_input_test
run_service_account_validation_test
run_server_host_validation_test
run_protected_web_dir_test
run_share_dir_overlap_test
run_web_dir_overlap_test
run_core_path_overlap_test
run_protected_config_and_storage_test
run_config_path_scope_test
run_systemd_specifier_rejection_test
run_symlink_path_rejection_test
run_systemd_newline_rejection_test
run_dataplane_addr_validation_test
run_doctor_config_test
run_doctor_input_validation_test

printf '[systemd-install-test] all checks passed\n'
