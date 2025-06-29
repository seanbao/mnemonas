#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
declare -a VICTIM_PIDS=()

cleanup() {
  local pid
  for pid in "${VICTIM_PIDS[@]}"; do
    kill "$pid" >/dev/null 2>&1 || true
    wait "$pid" 2>/dev/null || true
  done
  rm -rf -- "$TMP_ROOT"
}
trap cleanup EXIT

fail() {
  printf '[dev-safety-test] ERROR: %s\n' "$*" >&2
  exit 1
}

run_unrelated_pid_test() {
  local case_dir="$TMP_ROOT/unrelated-pid"
  mkdir -p "$case_dir/pids" "$case_dir/logs"

  (
    cd "$case_dir"
    sleep 60
  ) &
  local victim=$!
  VICTIM_PIDS+=("$victim")

  printf '%s\n' "$victim" > "$case_dir/pids/nasd.pid"

  MNEMONAS_DEV_PID_DIR="$case_dir/pids" \
    MNEMONAS_DEV_LOG_DIR="$case_dir/logs" \
    bash "$REPO_ROOT/scripts/dev.sh" --kill > "$case_dir/out.log" 2>&1

  if ! kill -0 "$victim" 2>/dev/null; then
    fail "dev --kill stopped an unrelated PID from a tampered nasd.pid"
  fi

  if [[ -e "$case_dir/pids/nasd.pid" ]]; then
    fail "dev --kill did not remove stale tampered nasd.pid"
  fi
}

run_invalid_pid_file_test() {
  local case_dir="$TMP_ROOT/invalid-pid"
  mkdir -p "$case_dir/pids" "$case_dir/logs"

  printf 'not-a-pid\n' > "$case_dir/pids/frontend.pid"

  MNEMONAS_DEV_PID_DIR="$case_dir/pids" \
    MNEMONAS_DEV_LOG_DIR="$case_dir/logs" \
    bash "$REPO_ROOT/scripts/dev.sh" --kill > "$case_dir/out.log" 2>&1

  if [[ -e "$case_dir/pids/frontend.pid" ]]; then
    fail "dev --kill did not remove invalid frontend.pid"
  fi
}

run_creds_hides_webdav_password_by_default() {
  local case_dir="$TMP_ROOT/creds"
  local fake_home="$case_dir/home"
  local secret="super-secret-webdav"
  mkdir -p "$case_dir/pids" "$case_dir/logs" "$fake_home/.mnemonas"
  printf '{"webdav_password": "%s"}\n' "$secret" > "$fake_home/.mnemonas/secrets.json"

  HOME="$fake_home" \
    MNEMONAS_DEV_PID_DIR="$case_dir/pids" \
    MNEMONAS_DEV_LOG_DIR="$case_dir/logs" \
    bash "$REPO_ROOT/scripts/dev.sh" --creds > "$case_dir/hidden.log" 2>&1

  if grep -q "$secret" "$case_dir/hidden.log"; then
    fail "dev --creds printed the WebDAV password without MNEMONAS_DEV_SHOW_SECRETS"
  fi
  if ! grep -q "密码:   已隐藏" "$case_dir/hidden.log"; then
    fail "dev --creds did not explain that the WebDAV password is hidden by default"
  fi

  HOME="$fake_home" \
    MNEMONAS_DEV_SHOW_SECRETS=1 \
    MNEMONAS_DEV_PID_DIR="$case_dir/pids" \
    MNEMONAS_DEV_LOG_DIR="$case_dir/logs" \
    bash "$REPO_ROOT/scripts/dev.sh" --creds > "$case_dir/revealed.log" 2>&1

  if ! grep -q "$secret" "$case_dir/revealed.log"; then
    fail "dev --creds did not print the WebDAV password when MNEMONAS_DEV_SHOW_SECRETS=1"
  fi
}

run_creds_decodes_json_secret_test() {
  local case_dir="$TMP_ROOT/creds-json-secret"
  local fake_home="$case_dir/home"
  local secret='quote"slash\value'
  mkdir -p "$case_dir/pids" "$case_dir/logs" "$fake_home/.mnemonas"
  printf '{"webdav_password": "quote\\"slash\\\\value"}\n' > "$fake_home/.mnemonas/secrets.json"

  HOME="$fake_home" \
    MNEMONAS_DEV_SHOW_SECRETS=1 \
    MNEMONAS_DEV_PID_DIR="$case_dir/pids" \
    MNEMONAS_DEV_LOG_DIR="$case_dir/logs" \
    bash "$REPO_ROOT/scripts/dev.sh" --creds > "$case_dir/revealed.log" 2>&1

  if ! grep -Fq "$secret" "$case_dir/revealed.log"; then
    fail "dev --creds did not decode JSON-escaped WebDAV password"
  fi
}

run_creds_decodes_toml_config_secret_test() {
  local case_dir="$TMP_ROOT/creds-toml-secret"
  local fake_home="$case_dir/home"
  local secret='quote"slash\value'
  mkdir -p "$case_dir/pids" "$case_dir/logs" "$fake_home/.mnemonas"
  cat > "$fake_home/.mnemonas/config.toml" <<EOF
[webdav]
username = "admin"
password = "quote\\"slash\\\\value"
EOF

  HOME="$fake_home" \
    MNEMONAS_DEV_SHOW_SECRETS=1 \
    MNEMONAS_DEV_PID_DIR="$case_dir/pids" \
    MNEMONAS_DEV_LOG_DIR="$case_dir/logs" \
    bash "$REPO_ROOT/scripts/dev.sh" --creds > "$case_dir/revealed.log" 2>&1

  if ! grep -Fq "$secret" "$case_dir/revealed.log"; then
    fail "dev --creds did not decode TOML-escaped WebDAV password"
  fi
}

run_unrelated_pid_test
run_invalid_pid_file_test
run_creds_hides_webdav_password_by_default
run_creds_decodes_json_secret_test
run_creds_decodes_toml_config_secret_test

printf '[dev-safety-test] all checks passed\n'
