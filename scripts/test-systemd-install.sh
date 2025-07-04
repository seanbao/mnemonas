#!/usr/bin/env bash
# shellcheck disable=SC2016

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "$TMP_ROOT"' EXIT

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
  local storage_dir="$case_dir/custom-storage"
  mkdir -p "$install_dir/etc/mnemonas" "$storage_dir"
  make_fake_admin_path "$fake_path"
  make_release_tree "$release_dir"

  cat > "$install_dir/etc/mnemonas/config.toml" <<EOF
[server]
host = "127.0.0.1"
port = 18080

[ storage ] # storage root may have comments in hand-edited TOML
root = "$storage_dir"

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

run_fresh_install_test
run_existing_config_test
run_invalid_input_test
run_doctor_config_test

printf '[systemd-install-test] all checks passed\n'
